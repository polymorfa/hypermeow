// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/types"
)

type blockingDeviceContainer struct {
	putStarted    chan struct{}
	allowPut      chan struct{}
	deleteStarted chan struct{}
}

type legacyLIDStore struct{}

func (*legacyLIDStore) PutManyLIDMappings(context.Context, []LIDMapping) error { return nil }
func (*legacyLIDStore) PutLIDMapping(context.Context, types.JID, types.JID) error {
	return nil
}
func (*legacyLIDStore) GetPNForLID(context.Context, types.JID) (types.JID, error) {
	return types.EmptyJID, nil
}
func (*legacyLIDStore) GetLIDForPN(context.Context, types.JID) (types.JID, error) {
	return types.EmptyJID, nil
}
func (*legacyLIDStore) GetManyLIDsForPNs(context.Context, []types.JID) (map[types.JID]types.JID, error) {
	return nil, nil
}

var _ LIDStore = (*legacyLIDStore)(nil)
var _ LIDBatchReverseStore = (*NoopStore)(nil)

func (c *blockingDeviceContainer) PutDevice(_ context.Context, device *Device) error {
	close(c.putStarted)
	<-c.allowPut
	if device.ID == nil {
		return errors.New("device ID cleared during save")
	}
	return nil
}

func (c *blockingDeviceContainer) DeleteDevice(context.Context, *Device) error {
	close(c.deleteStarted)
	return nil
}

func TestDeviceDeleteWaitsForSave(t *testing.T) {
	container := &blockingDeviceContainer{
		putStarted:    make(chan struct{}),
		allowPut:      make(chan struct{}),
		deleteStarted: make(chan struct{}),
	}
	id := types.NewJID("15551234567", types.DefaultUserServer)
	device := &Device{ID: &id, Container: container}
	saveDone := make(chan error, 1)
	deleteDone := make(chan error, 1)

	go func() { saveDone <- device.Save(context.Background()) }()
	<-container.putStarted
	go func() { deleteDone <- device.Delete(context.Background()) }()

	select {
	case <-container.deleteStarted:
		close(container.allowPut)
		<-saveDone
		<-deleteDone
		t.Fatal("delete entered the container while save was active")
	case <-time.After(50 * time.Millisecond):
	}

	close(container.allowPut)
	if err := <-saveDone; err != nil {
		t.Fatal(err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	if !device.Deleted || device.ID != nil {
		t.Fatalf("device was not deleted: deleted=%t id=%v", device.Deleted, device.ID)
	}
}

func TestDeviceDeleteWaitObservesCancellation(t *testing.T) {
	container := &blockingDeviceContainer{
		putStarted:    make(chan struct{}),
		allowPut:      make(chan struct{}),
		deleteStarted: make(chan struct{}),
	}
	id := types.NewJID("15551234567", types.DefaultUserServer)
	device := &Device{ID: &id, Container: container}
	saveDone := make(chan error, 1)
	go func() { saveDone <- device.Save(context.Background()) }()
	<-container.putStarted
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- device.Delete(ctx) }()
	select {
	case err := <-deleteDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("delete error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled delete remained blocked")
	}
	select {
	case <-container.deleteStarted:
		t.Fatal("canceled delete entered the container")
	default:
	}
	close(container.allowPut)
	if err := <-saveDone; err != nil {
		t.Fatal(err)
	}
}

func TestDeviceLockRejectsCanceledContextWhenAvailable(t *testing.T) {
	device := &Device{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 100 {
		err := device.lockSaveDelete(ctx)
		if err == nil {
			device.unlockSaveDelete()
			t.Fatal("canceled context acquired the save/delete lock")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lock error = %v, want context canceled", err)
		}
	}
}

func TestDeviceSaveAfterDeleteFailsWithoutContainerWrite(t *testing.T) {
	container := &blockingDeviceContainer{
		putStarted:    make(chan struct{}),
		allowPut:      make(chan struct{}),
		deleteStarted: make(chan struct{}),
	}
	close(container.allowPut)
	id := types.NewJID("15551234567", types.DefaultUserServer)
	device := &Device{ID: &id, Container: container}
	if err := device.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := device.Save(context.Background()); !errors.Is(err, ErrDeviceDeleted) {
		t.Fatalf("save error = %v, want %v", err, ErrDeviceDeleted)
	}
	select {
	case <-container.putStarted:
		t.Fatal("save wrote to the container after delete")
	default:
	}
}
