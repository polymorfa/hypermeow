// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/polymorfa/libsignal-protocol-go/protocol"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

func TestEncryptRejectsCacheLoadedBeforeIdentityReset(t *testing.T) {
	for _, status := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "status"}[status], func(t *testing.T) {
			own := types.NewADJID("100000000001", 0, 1)
			peerJID := types.NewADJID("200000000002", 0, 2)
			companion, _ := newSignalTestDevice(own, &store.NoopStore{})
			peer, _ := newSignalTestDevice(peerJID, &store.NoopStore{})
			cli := NewClient(companion, waLog.Noop)
			ctx := context.Background()
			if status {
				ctx = store.WithStatusSession(ctx)
			}
			if _, _, err := cli.encryptMessageForDevice(ctx, []byte("authored initial"), peerJID, signalTestBundle(t, peer, 2), nil, nil); err != nil {
				t.Fatal(err)
			}
			address := store.SignalSessionAddress(ctx, peerJID.SignalAddress().String())
			existing, cached, err := companion.WithCachedSessions(ctx, []string{address})
			if err != nil {
				t.Fatal(err)
			}
			if err = companion.DeleteSessionsForIdentity(context.Background(), peerJID.SignalAddress()); err != nil {
				t.Fatal(err)
			}
			if _, _, err = cli.encryptMessageForDevice(cached, []byte("authored stale"), peerJID, nil, nil, existing); err == nil {
				t.Fatal("encrypted with a cached ratchet from before the identity reset")
			}
		})
	}
}

type guardOrderBuffer struct {
	store.EventBuffer
	entered chan struct{}
}

func (b *guardOrderBuffer) GetBufferedEvent(context.Context, [32]byte) (*store.BufferedEvent, error) {
	return nil, nil
}
func (b *guardOrderBuffer) PutBufferedEvent(context.Context, [32]byte, []byte, time.Time) error {
	return nil
}
func (b *guardOrderBuffer) DoDecryptionTxn(ctx context.Context, fn func(context.Context) error) error {
	close(b.entered)
	return fn(ctx)
}

func TestBufferedDecryptOrdersTransactionAfterSessionMutation(t *testing.T) {
	device, _ := newSignalTestDevice(types.NewADJID("100000000001", 0, 1), &store.NoopStore{})
	buffer := &guardOrderBuffer{entered: make(chan struct{})}
	device.EventBuffer = buffer
	cli := NewClient(device, waLog.Noop)
	cli.EnableDecryptedEventBuffer = true
	locker, ok := any(device).(interface {
		WithSessionStateLock(context.Context, func(context.Context) error) error
	})
	if !ok {
		t.Fatal("local session mutation guard is unavailable")
	}
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	guarded := make(chan error, 1)
	go func() {
		guarded <- locker.WithSessionStateLock(context.Background(), func(context.Context) error { close(held); <-release; return nil })
	}()
	<-held
	done := make(chan error, 1)
	go func() {
		_, _, err := cli.bufferedDecrypt(context.Background(), []byte("authored guard order"), time.Unix(1700000000, 0), func(ctx context.Context) ([]byte, error) {
			return []byte("authored plaintext"), device.DeleteSessionsForIdentity(ctx, protocol.NewSignalAddress("123", 1))
		})
		done <- err
	}()
	early := false
	select {
	case <-buffer.entered:
		early = true
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	if err := <-guarded; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nested reset deadlocked")
	}
	if early {
		t.Fatal("database transaction entered before local mutation guard")
	}
}
