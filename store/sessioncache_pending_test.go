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

func TestFailedIdentityResetBudgetNeverForgetsUnsafeRows(t *testing.T) {
	backend := &resetSessionStore{rows: make(map[string][]byte)}
	device := &Device{Sessions: backend}
	for i := range maxSessionResetAddresses + 1 {
		address := protocol.NewSignalAddress(fmt.Sprintf("authored-%d", i), 1)
		backend.failDelete = address.String() + "_status"
		if err := device.DeleteSessionsForIdentity(context.Background(), address); !errors.Is(err, ErrSessionCacheInvalidated) {
			t.Fatalf("failed reset %d was not refused: %v", i, err)
		}
	}
	if len(device.sessionResetPending) > maxSessionResetAddresses || len(device.sessionResetAddresses) > maxSessionResetAddresses {
		t.Fatal("failed reset tracking exceeded its budgets")
	}
	if !device.sessionResetBlocked {
		t.Fatal("exhausted failure budget did not refuse unknown state")
	}
	backend.failDelete = ""
	if err := device.DeleteSessionsForIdentity(context.Background(), protocol.NewSignalAddress("authored-0", 1)); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatalf("an unrelated successful retry cleared unknown failures: %v", err)
	}
	if _, _, err := device.WithCachedSessions(context.Background(), []string{"fresh:1"}); !errors.Is(err, ErrSessionCacheInvalidated) {
		t.Fatalf("failure budget overflow allowed a fresh cache: %v", err)
	}
}
