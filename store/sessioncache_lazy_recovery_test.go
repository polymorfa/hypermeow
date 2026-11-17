// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/polymorfa/libsignal-protocol-go/protocol"
)

func TestRecoveredBackendIsReconciledBeforeSessionRead(t *testing.T) {
	for _, method := range []string{"prefetch", "load", "lookup", "crypto check"} {
		t.Run(method, func(t *testing.T) {
			ctx := WithStatusSession(context.Background())
			address := protocol.NewSignalAddress("123", 1)
			key := address.String() + "_status"
			backend := &resetSessionStore{rows: map[string][]byte{key: cacheTestSession().Serialize()}, failDelete: key}
			device := &Device{Sessions: backend}
			if err := device.DeleteSessionsForIdentity(ctx, address); !errors.Is(err, ErrSessionCacheInvalidated) {
				t.Fatal(err)
			}
			backend.failDelete = ""
			var err error
			switch method {
			case "prefetch":
				var found map[string]bool
				found, _, err = device.WithCachedSessions(ctx, []string{key})
				if found[key] {
					t.Error("prefetch returned the failed-deletion row")
				}
			case "load":
				_, err = device.LoadSession(ctx, address)
			case "lookup":
				_, err = device.ContainsSession(ctx, address)
			case "crypto check":
				err = device.CheckCachedSession(ctx, key)
			}
			if err != nil {
				t.Errorf("recovered backend remains unavailable to %s: %v", method, err)
			}
			if _, found := backend.rows[key]; found {
				t.Error("read did not confirm deletion of its pending row")
			}
			if len(device.sessionResetPending) != 0 {
				t.Error("confirmed deletion remains pending")
			}
		})
	}
}
