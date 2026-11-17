// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"errors"
	"testing"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

type recoverySignalStore struct {
	*memSignalStore
	failDelete      string
	identityDeletes int
}

func (s *recoverySignalStore) DeleteIdentity(_ context.Context, address string) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.identityDeletes++
	delete(s.identities, address)
	return nil
}
func (s *recoverySignalStore) IsTrustedIdentity(_ context.Context, address string, key [32]byte) (bool, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	old, found := s.identities[address]
	return !found || old == key, nil
}
func (s *recoverySignalStore) DeleteSession(ctx context.Context, address string) error {
	if address == s.failDelete {
		return errors.New("authored deletion failure")
	}
	return s.memSignalStore.DeleteSession(ctx, address)
}

// A fresh adapter shares an authored persistent-store model. This is not a
// process crash, previous-release database fixture or live WhatsApp replay.
func TestFailedIdentityResetKeepsRestartedRowsBoundToTheirIdentity(t *testing.T) {
	for _, suffix := range []string{"", "_status"} {
		t.Run("failed"+suffix, func(t *testing.T) {
			ctx := context.Background()
			own := types.NewADJID("100000000001", 0, 1)
			peerJID := types.NewADJID("200000000002", 0, 2)
			device, memory := newSignalTestDevice(own, &store.NoopStore{})
			backend := &recoverySignalStore{memSignalStore: memory}
			device.Sessions, device.Identities = backend, backend
			peer, _ := newSignalTestDevice(peerJID, &store.NoopStore{})
			client := NewClient(device, waLog.Noop)
			for i, scope := range []context.Context{ctx, store.WithStatusSession(ctx)} {
				if _, _, err := client.encryptMessageForDevice(scope, []byte("authored initial"), peerJID, signalTestBundle(t, peer, uint32(i+2)), nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			address := peerJID.SignalAddress().String()
			old := memory.identities[address]
			backend.failDelete = address + suffix
			if err := client.clearUntrustedIdentity(ctx, peerJID); err == nil {
				t.Fatal("deletion fault was not returned")
			}
			if backend.identityDeletes != 0 {
				t.Error("identity was deleted before both session scopes were removed")
			}
			fresh := &store.Device{Sessions: backend, Identities: backend}
			if _, found := memory.identities[address]; !found {
				t.Error("fresh adapter has a surviving row without its identity")
			}
			if _, err := fresh.LoadSession(map[bool]context.Context{false: ctx, true: store.WithStatusSession(ctx)}[suffix != ""], peerJID.SignalAddress()); err != nil {
				t.Fatal(err)
			}
			other := old
			other[0] ^= 1
			if trusted, err := fresh.Identities.IsTrustedIdentity(ctx, address, other); err != nil || trusted {
				t.Errorf("fresh adapter accepts replacement identity after failed reset: %v", err)
			}
			backend.failDelete = ""
			if err := client.clearUntrustedIdentity(ctx, peerJID); err != nil {
				t.Fatal(err)
			}
			if len(memory.sessions) != 0 || len(memory.identities) != 0 {
				t.Fatal("successful retry retained old identity/session rows")
			}
		})
	}
}
