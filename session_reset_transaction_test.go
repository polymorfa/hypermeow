// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

type pendingRollbackRowsKey struct{}
type pendingRollbackStore struct {
	*recoverySignalStore
	transactionDeletes int
}

func (s *pendingRollbackStore) DeleteSession(ctx context.Context, key string) error {
	if rows, ok := ctx.Value(pendingRollbackRowsKey{}).(map[string][]byte); ok {
		s.transactionDeletes++
		delete(rows, key)
		return nil
	}
	return s.recoverySignalStore.DeleteSession(ctx, key)
}
func (s *pendingRollbackStore) HasSession(ctx context.Context, key string) (bool, error) {
	if rows, ok := ctx.Value(pendingRollbackRowsKey{}).(map[string][]byte); ok {
		_, found := rows[key]
		return found, nil
	}
	return s.memSignalStore.HasSession(ctx, key)
}

type pendingRollbackBuffer struct {
	store.EventBuffer
	backend *pendingRollbackStore
}

func (*pendingRollbackBuffer) GetBufferedEvent(context.Context, [32]byte) (*store.BufferedEvent, error) {
	return nil, nil
}
func (*pendingRollbackBuffer) PutBufferedEvent(context.Context, [32]byte, []byte, time.Time) error {
	return nil
}
func (b *pendingRollbackBuffer) DoDecryptionTxn(ctx context.Context, fn func(context.Context) error) error {
	rows := maps.Clone(b.backend.sessions)
	if err := fn(context.WithValue(ctx, pendingRollbackRowsKey{}, rows)); err != nil {
		return err // Authored rollback model: discard transactional DELETEs.
	}
	b.backend.sessions = rows
	return nil
}

func TestBufferedRollbackCannotForgetAPendingSessionDeletion(t *testing.T) {
	ctx := store.WithStatusSession(context.Background())
	peer := types.NewADJID("200000000002", 0, 2)
	key := peer.SignalAddress().String() + "_status"
	backend := &pendingRollbackStore{recoverySignalStore: &recoverySignalStore{memSignalStore: newMemSignalStore(), failDelete: key}}
	// Opaque authored rows: only HasSession is used, not protocol decoding.
	backend.sessions[key] = []byte("authored pending row")
	device := &store.Device{Sessions: backend}
	if err := device.DeleteSessionsForIdentity(ctx, peer.SignalAddress()); !errors.Is(err, store.ErrSessionCacheInvalidated) {
		t.Fatal(err)
	}
	backend.failDelete = ""
	device.EventBuffer = &pendingRollbackBuffer{backend: backend}
	client := NewClient(device, waLog.Noop)
	client.EnableDecryptedEventBuffer = true
	fault := errors.New("authored decryption failure after lookup")
	_, _, err := client.bufferedDecrypt(ctx, []byte("authored hash input, not a packet"), time.Unix(1700000000, 0), func(tx context.Context) ([]byte, error) {
		if _, readErr := device.ContainsSession(tx, peer.SignalAddress()); readErr != nil {
			return nil, readErr
		}
		return nil, fault
	})
	if err == nil {
		t.Fatal("decryption failure was not returned")
	}
	if backend.transactionDeletes != 0 {
		t.Error("pending deletion was reconciled inside a rollbackable transaction")
	}
	if _, found := backend.sessions[key]; !found {
		t.Fatal("rollback model failed to retain the original row")
	}
	found, err := device.ContainsSession(ctx, peer.SignalAddress())
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("rollback forgot the pending marker and accepted the old row")
	}
	if _, found = backend.sessions[key]; found {
		t.Error("later nontransactional access did not confirm deletion")
	}
}

func TestPreparedBufferedFailureCannotRollbackSessionRecovery(t *testing.T) {
	ctx := context.Background()
	peer := types.NewADJID("200000000002", 0, 2)
	key := peer.SignalAddress().String()
	backend := &pendingRollbackStore{recoverySignalStore: &recoverySignalStore{memSignalStore: newMemSignalStore(), failDelete: key}}
	backend.sessions[key] = []byte("authored pending sibling row")
	device := &store.Device{Sessions: backend}
	if err := device.DeleteSessionsForIdentity(ctx, peer.SignalAddress()); !errors.Is(err, store.ErrSessionCacheInvalidated) {
		t.Fatal(err)
	}
	backend.failDelete = ""
	device.EventBuffer = &pendingRollbackBuffer{backend: backend}
	client := NewClient(device, waLog.Noop)
	client.EnableDecryptedEventBuffer = true
	fault := errors.New("authored post-recovery decryption failure")
	_, _, err := client.bufferedDecryptPrepared(store.WithStatusSession(ctx), []byte("authored hash input"), time.Unix(1700000000, 0), func(prepareCtx context.Context) error {
		return device.PrepareIdentitySessions(prepareCtx, peer.SignalAddress())
	}, func(tx context.Context) ([]byte, error) {
		return nil, fault
	})
	if !errors.Is(err, fault) {
		t.Fatalf("lost decryption failure: %v", err)
	}
	if backend.transactionDeletes != 0 {
		t.Error("preparation ran inside the transaction")
	}
	if _, found := backend.sessions[key]; found {
		t.Error("decryption rollback restored the prepared sibling deletion")
	}
}

type preparedReplayBuffer struct{ store.EventBuffer }

func (*preparedReplayBuffer) GetBufferedEvent(context.Context, [32]byte) (*store.BufferedEvent, error) {
	return &store.BufferedEvent{Plaintext: []byte("authored saved plaintext")}, nil
}
func TestBufferedReplayDoesNotPrepareOrDecryptAgain(t *testing.T) {
	device := &store.Device{EventBuffer: &preparedReplayBuffer{}}
	client := NewClient(device, waLog.Noop)
	client.EnableDecryptedEventBuffer = true
	got, _, err := client.bufferedDecryptPrepared(context.Background(), []byte("authored hash input"), time.Unix(1700000000, 0), func(context.Context) error {
		t.Error("saved replay prepared sessions again")
		return nil
	}, func(context.Context) ([]byte, error) {
		t.Error("saved replay decrypted again")
		return nil, nil
	})
	if err != nil || string(got) != "authored saved plaintext" {
		t.Fatalf("saved replay changed: %v", err)
	}
}
