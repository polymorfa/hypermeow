// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/polymorfa/libsignal-protocol-go/protocol"
)

type outageSessionStore struct {
	resetSessionStore
	failAll  bool
	attempts map[string]int
}

func (s *outageSessionStore) DeleteSession(ctx context.Context, address string) error {
	s.attempts[address]++
	if s.failAll {
		return errors.New("authored backend outage")
	}
	return s.resetSessionStore.DeleteSession(ctx, address)
}

func TestPendingResetRecoveryRetainsEveryStartedFailure(t *testing.T) {
	backend := &outageSessionStore{resetSessionStore: resetSessionStore{rows: make(map[string][]byte)}, failAll: true, attempts: make(map[string]int)}
	device := &Device{Sessions: backend}
	for i := range maxSessionResetAddresses / 2 {
		if err := device.DeleteSessionsForIdentity(context.Background(), protocol.NewSignalAddress(fmt.Sprintf("authored-%d", i), 1)); !errors.Is(err, ErrSessionCacheInvalidated) {
			t.Fatal(err)
		}
	}
	fresh := protocol.NewSignalAddress("fresh", 1)
	if err := device.DeleteSessionsForIdentity(context.Background(), fresh); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatalf("full failure budget admitted a new reset: %v", err)
	}
	if backend.attempts[fresh.String()] != 0 || backend.attempts[fresh.String()+"_status"] != 0 {
		t.Error("reset started without room to retain all failures")
	}
	if len(device.sessionResetPending) != maxSessionResetAddresses {
		t.Fatalf("lost tracked failures: %d", len(device.sessionResetPending))
	}
	recovery, ok := any(device).(interface{ ReconcileSessionResets(context.Context) error })
	if !ok {
		t.Fatal("no explicit pending-reset recovery path")
	}
	if err := recovery.ReconcileSessionResets(context.Background()); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatalf("outage recovery did not retain refusal: %v", err)
	}
	backend.failAll = false
	if err := recovery.ReconcileSessionResets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(device.sessionResetPending) != 0 {
		t.Fatal("successful recovery retained failed keys")
	}
	if err := device.DeleteSessionsForIdentity(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, _, err := device.WithCachedSessions(context.Background(), []string{fresh.String()}); err != nil {
		t.Fatalf("recovered Device remains blocked: %v", err)
	}
}
