// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package socket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	waLog "github.com/polymorfa/hypermeow/util/log"
)

func newTestFrameSocket(t *testing.T) *FrameSocket {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("Failed to accept websocket: %v", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		<-conn.CloseRead(r.Context()).Done()
	}))
	t.Cleanup(server.Close)
	fs := NewFrameSocket(waLog.Noop, server.Client())
	fs.URL = server.URL
	if err := fs.Connect(t.Context()); err != nil {
		t.Fatalf("Failed to connect websocket: %v", err)
	}
	t.Cleanup(func() { fs.Close(0) })
	return fs
}

func TestNoiseSocketStopConcurrentClose(t *testing.T) {
	for range 100 {
		fs := newTestFrameSocket(t)
		ns, err := newNoiseSocket(t.Context(), fs, nil, nil, nil, func(context.Context, *NoiseSocket, bool) {})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Go(func() {
			<-start
			fs.Close(0)
		})
		wg.Go(func() {
			<-start
			ns.Stop(true, false)
		})
		close(start)
		wg.Wait()
	}
}

func TestNewNoiseSocketConcurrentClose(t *testing.T) {
	for range 100 {
		fs := newTestFrameSocket(t)
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Go(func() {
			<-start
			fs.Close(0)
		})
		close(start)
		ns, err := newNoiseSocket(t.Context(), fs, nil, nil, nil, func(context.Context, *NoiseSocket, bool) {})
		if err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		ns.Stop(false, false)
	}
}

func TestNoiseSocketStop(t *testing.T) {
	for _, tc := range []struct {
		name              string
		disconnect        bool
		allowOnDisconnect bool
	}{
		{name: "local suppressed", disconnect: true},
		{name: "local allowed", disconnect: true, allowOnDisconnect: true},
		{name: "remote suppressed"},
		{name: "remote allowed", allowOnDisconnect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := newTestFrameSocket(t)
			called := make(chan bool, 2)
			ns, err := newNoiseSocket(t.Context(), fs, nil, nil, nil, func(ctx context.Context, socket *NoiseSocket, remote bool) {
				if ctx != t.Context() {
					t.Error("Unexpected disconnect context")
				}
				socket.Stop(false, false)
				fs.Close(0)
				called <- remote
			})
			if err != nil {
				t.Fatal(err)
			}
			ns.Stop(tc.disconnect, tc.allowOnDisconnect)
			if fs.IsConnected() == tc.disconnect {
				t.Fatalf("Unexpected connection state after Stop: %t", fs.IsConnected())
			}
			select {
			case <-ns.stopConsumer:
			default:
				t.Fatal("Frame consumer was not stopped")
			}
			fs.lock.Lock()
			hasHandler := fs.OnDisconnect != nil
			fs.lock.Unlock()
			if hasHandler != tc.allowOnDisconnect {
				t.Fatalf("Unexpected disconnect handler after Stop: %t", hasHandler)
			}
			fs.Close(0)
			fs.Close(0)
			if tc.allowOnDisconnect {
				select {
				case remote := <-called:
					if remote == tc.disconnect {
						t.Errorf("Unexpected remote flag: %t", remote)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("Disconnect handler did not return")
				}
			}
			select {
			case <-called:
				t.Fatal("Unexpected disconnect callback")
			default:
			}
		})
	}
}

func TestNoiseSocketRemoteDisconnect(t *testing.T) {
	fs := newTestFrameSocket(t)
	called := make(chan bool, 1)
	ns, err := newNoiseSocket(t.Context(), fs, nil, nil, nil, func(ctx context.Context, socket *NoiseSocket, remote bool) {
		socket.Stop(false, false)
		fs.Close(0)
		called <- remote
	})
	if err != nil {
		t.Fatal(err)
	}
	fs.Close(0)
	select {
	case remote := <-called:
		if !remote {
			t.Error("Expected remote disconnect")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Disconnect handler did not return")
	}
	if !ns.destroyed.Load() {
		t.Fatal("Disconnect handler did not stop the noise socket")
	}
}
