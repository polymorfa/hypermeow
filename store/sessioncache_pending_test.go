// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/polymorfa/libsignal-protocol-go/protocol"
)

func TestFailedIdentityResetAbortsTheCurrentSend(t *testing.T) {
	address := protocol.NewSignalAddress("123", 1)
	backend := &resetSessionStore{rows: make(map[string][]byte), failDelete: address.String() + "_status"}
	device := &Device{Sessions: backend}
	if err := device.DeleteSessionsForIdentity(context.Background(), address); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatalf("failed reset is not fatal to recipient fan-out: %v", err)
	}
	if err := device.CheckCachedSession(context.Background(), "unrelated:2"); err != nil {
		t.Fatalf("one failed key blocked an unrelated session: %v", err)
	}
	backend.failDelete = ""
	if err := device.DeleteSession(WithStatusSession(context.Background()), address); err != nil {
		t.Fatal(err)
	}
	if err := device.CheckCachedSession(context.Background(), address.String()+"_status"); err != nil {
		t.Fatalf("successful explicit deletion did not clear its pending mark: %v", err)
	}
}

func TestFailedIdentityResetBudgetRetainsAndRecoversUnsafeRows(t *testing.T) {
	backend := &resetSessionStore{rows: make(map[string][]byte)}
	device := &Device{Sessions: backend}
	for i := range maxSessionResetAddresses + 1 {
		address := protocol.NewSignalAddress(fmt.Sprintf("authored-%d", i), 1)
		backend.failDelete = address.String() + "_status"
		if err := device.DeleteSessionsForIdentity(context.Background(), address); !errors.Is(err, ErrSessionCacheInvalidated) {
			t.Fatalf("failed reset %d was not refused: %v", i, err)
		}
		if _, pending := device.sessionResetPending[backend.failDelete]; !pending {
			t.Fatal("actual failed deletion was forgotten")
		}
	}
	if len(device.sessionResetPending) > maxSessionResetAddresses || len(device.sessionResetAddresses) > maxSessionResetAddresses {
		t.Fatal("failed reset tracking exceeded its budgets")
	}
	backend.failDelete = ""
	if err := device.ReconcileSessionResets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(device.sessionResetPending) != 0 {
		t.Fatal("successful reconciliation retained failed keys")
	}
	if _, _, err := device.WithCachedSessions(context.Background(), []string{"fresh:1"}); err != nil {
		t.Fatalf("recovered Device remains blocked: %v", err)
	}
}

func TestSuccessfulIdentityResetDoesNotSpendFailureBudget(t *testing.T) {
	backend := &resetSessionStore{rows: make(map[string][]byte)}
	device := &Device{Sessions: backend}
	for i := range maxSessionResetAddresses - 1 {
		address := protocol.NewSignalAddress(fmt.Sprintf("authored-%d", i), 1)
		backend.failDelete = address.String() + "_status"
		if err := device.DeleteSessionsForIdentity(context.Background(), address); !errors.Is(err, ErrSessionCacheInvalidated) {
			t.Fatal(err)
		}
	}
	backend.failDelete = ""
	if err := device.DeleteSessionsForIdentity(context.Background(), protocol.NewSignalAddress("fresh", 1)); err != nil {
		t.Errorf("successful reset exhausted the failure budget: %v", err)
	}
	for i := range maxSessionResetAddresses - 1 {
		if err := device.DeleteSession(WithStatusSession(context.Background()), protocol.NewSignalAddress(fmt.Sprintf("authored-%d", i), 1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := device.CheckCachedSession(context.Background(), "unrelated:1"); err != nil {
		t.Errorf("tracked failures left the recovered Device blocked: %v", err)
	}
}
