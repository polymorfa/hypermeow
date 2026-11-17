// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/polymorfa/libsignal-protocol-go/protocol"
)

type resetIdentityFaultStore struct {
	NoopStore
	deleteIdentity func() error
}

func (s *resetIdentityFaultStore) DeleteIdentity(context.Context, string) error {
	return s.deleteIdentity()
}

func TestIdentityDeleteFaultCannotRestoreEitherSessionScope(t *testing.T) {
	ctx := context.Background()
	address := protocol.NewSignalAddress("123", 1)
	backend := &resetSessionStore{rows: map[string][]byte{address.String(): cacheTestSession().Serialize(), address.String() + "_status": cacheTestSession().Serialize()}}
	fault := errors.New("authored identity deletion failure")
	device := &Device{Sessions: backend, Identities: &resetIdentityFaultStore{deleteIdentity: func() error {
		if len(backend.rows) != 0 {
			t.Error("identity deletion started before session deletion finished")
		}
		return fault
	}}}
	_, cached, err := device.WithCachedSessions(ctx, []string{address.String()})
	if err != nil {
		t.Fatal(err)
	}
	if err = device.StoreSession(cached, address, cacheTestSession()); err != nil {
		t.Fatal(err)
	}
	if err = device.DeleteIdentityAndSessions(ctx, address); !errors.Is(err, fault) || !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatalf("identity deletion error lost its cause or fatal classification: %v", err)
	}
	if err = device.PutCachedSessions(cached); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatalf("failed identity deletion allowed an old cache to flush: %v", err)
	}
	if len(backend.rows) != 0 {
		t.Fatal("old cache restored a deleted session")
	}
}

type nestedResetSessionStore struct {
	resetSessionStore
	hook  func(context.Context)
	calls int
}

func (s *nestedResetSessionStore) DeleteSession(ctx context.Context, address string) error {
	s.calls++
	if s.hook != nil {
		hook := s.hook
		s.hook = nil
		hook(ctx)
	}
	return s.resetSessionStore.DeleteSession(ctx, address)
}

func TestReconciliationCannotConsumeAnActiveResetReservation(t *testing.T) {
	address := protocol.NewSignalAddress("123", 1)
	backend := &nestedResetSessionStore{resetSessionStore: resetSessionStore{rows: make(map[string][]byte), failDelete: address.String()}}
	device := &Device{Sessions: backend}
	backend.hook = func(ctx context.Context) {
		if err := device.ReconcileSessionResets(ctx); !errors.Is(err, ErrSessionCacheInvalidated) {
			t.Errorf("reconciliation consumed an active reset: %v", err)
		}
		if backend.calls != 1 {
			t.Error("reconciliation retried an active backend deletion")
		}
	}
	if err := device.DeleteSessionsForIdentity(context.Background(), address); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatal(err)
	}
	if _, pending := device.sessionResetPending[address.String()]; !pending {
		t.Fatal("active deletion failure was forgotten")
	}
}

func TestReconciliationCannotRetryItsOwnActiveDeletion(t *testing.T) {
	address := protocol.NewSignalAddress("123", 1)
	backend := &nestedResetSessionStore{resetSessionStore: resetSessionStore{rows: make(map[string][]byte), failDelete: address.String()}}
	device := &Device{Sessions: backend}
	if err := device.DeleteSessionsForIdentity(context.Background(), address); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatal(err)
	}
	backend.failDelete = ""
	before := backend.calls
	backend.hook = func(ctx context.Context) {
		if err := device.ReconcileSessionResets(ctx); !errors.Is(err, ErrSessionCacheInvalidated) {
			t.Errorf("nested reconciliation consumed its caller: %v", err)
		}
		if backend.calls != before+1 {
			t.Error("nested reconciliation repeated active backend deletion")
		}
	}
	if err := device.ReconcileSessionResets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(device.sessionResetPending) != 0 {
		t.Fatal("successful outer reconciliation kept pending state")
	}
}

func TestPendingDeletionCannotBeClearedInsideKnownTransaction(t *testing.T) {
	ctx := context.Background()
	address := protocol.NewSignalAddress("123", 1)
	key := address.String() + "_status"
	backend := &resetSessionStore{rows: map[string][]byte{key: cacheTestSession().Serialize()}, failDelete: key}
	device := &Device{Sessions: backend}
	if err := device.DeleteSessionsForIdentity(ctx, address); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatal(err)
	}
	backend.failDelete = ""
	tx := WithSessionTransaction(WithStatusSession(ctx))
	for _, attempt := range []func() error{
		func() error { return device.ReconcileSessionResets(tx) },
		func() error { return device.DeleteSession(tx, address) },
		func() error { return device.DeleteSessionsForIdentity(tx, address) },
		func() error { _, err := device.ContainsSession(tx, address); return err },
	} {
		if err := attempt(); !errors.Is(err, ErrSessionCacheInvalidated) {
			t.Fatalf("transaction cleared pending state: %v", err)
		}
		if _, pending := device.sessionResetPending[key]; !pending {
			t.Fatal("transaction forgot pending key")
		}
		if _, found := backend.rows[key]; !found {
			t.Fatal("transaction attempted pending deletion")
		}
	}
	if err := device.ReconcileSessionResets(ctx); err != nil {
		t.Fatal(err)
	}
	if len(device.sessionResetPending) != 0 {
		t.Fatal("nontransactional recovery did not complete")
	}
}
