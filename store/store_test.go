package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

type blockingDeviceContainer struct {
	putStarted    chan struct{}
	allowPut      chan struct{}
	deleteStarted chan struct{}
}

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
