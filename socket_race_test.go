// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/socket"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

func connectStatusError(t *testing.T, status int) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := socket.ConnectRace(ctx, waLog.Noop, http.DefaultClient, []string{"ws" + server.URL[4:]}, nil)
	var statusErr socket.ErrWithStatusCode
	if !errors.As(err, &statusErr) || statusErr.StatusCode != status {
		t.Fatalf("connect error = %v, want HTTP %d", err, status)
	}
	return err
}

func TestConnectRetryClassificationPreservesHTTPStatusPrecedence(t *testing.T) {
	forbidden := connectStatusError(t, http.StatusForbidden)
	unavailable := connectStatusError(t, http.StatusServiceUnavailable)
	network := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"single rejection", forbidden, false},
		{"single server error", unavailable, true},
		{"single network error", network, true},
		{"rejection before network", errors.Join(forbidden, network), false},
		{"network before rejection", errors.Join(network, forbidden), false},
		{"rejection before server error", errors.Join(forbidden, unavailable), false},
		{"server error before rejection", errors.Join(unavailable, forbidden), true},
		{"all rejected", errors.Join(forbidden, forbidden), false},
		{"no error", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableConnectError(tc.err); got != tc.want {
				t.Fatalf("retryable = %v, want %v: %v", got, tc.want, tc.err)
			}
		})
	}
}
