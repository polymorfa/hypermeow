//go:build !js

package socket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	waLog "github.com/polymorfa/hypermeow/util/log"
)

// wsServer accepts websocket upgrades and records how each connection was
// closed, so a test can assert the loser got the client's own code and reason.
type wsServer struct {
	*httptest.Server
	delay time.Duration

	mu      sync.Mutex
	closes  []closeRecord
	opened  int
	handled sync.WaitGroup
}

type closeRecord struct {
	code   websocket.StatusCode
	reason string
}

func newWSServer(t *testing.T, delay time.Duration) *wsServer {
	t.Helper()
	s := &wsServer{delay: delay}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.delay > 0 {
			time.Sleep(s.delay)
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		s.mu.Lock()
		s.opened++
		s.mu.Unlock()
		s.handled.Add(1)
		defer s.handled.Done()
		// Block until the peer closes, then record how.
		_, _, readErr := conn.Read(r.Context())
		status := websocket.CloseStatus(readErr)
		reason := ""
		var ce websocket.CloseError
		if errors.As(readErr, &ce) {
			reason = ce.Reason
		}
		s.mu.Lock()
		s.closes = append(s.closes, closeRecord{code: status, reason: reason})
		s.mu.Unlock()
		_ = conn.CloseNow()
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *wsServer) wsURL() string { return "ws" + s.URL[len("http"):] }

// awaitClose waits for the server to record one close and returns the record.
func (s *wsServer) awaitClose(t *testing.T) []closeRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.recorded(); len(got) > 0 {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the socket was never closed")
	return nil
}

func (s *wsServer) recorded() []closeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]closeRecord(nil), s.closes...)
}

// The close frame a loser sends is the client's own: code 1000 with the
// reason "loser socket" (WAWebOpenSocket.js:47). An empty reason where the
// client sends one is observable on the wire, so this is the behaviour, not a
// log detail.
func TestCloseWithReasonSendsTheClientsCloseFrame(t *testing.T) {
	server := newWSServer(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fs := NewFrameSocket(waLog.Noop, http.DefaultClient)
	fs.URL = server.wsURL()
	if err := fs.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	fs.CloseWithReason(websocket.StatusNormalClosure, LoserSocketCloseReason)

	closes := server.awaitClose(t)
	if closes[0].code != websocket.StatusNormalClosure {
		t.Errorf("close code = %d, want %d", closes[0].code, websocket.StatusNormalClosure)
	}
	if closes[0].reason != LoserSocketCloseReason {
		t.Errorf("close reason = %q, want %q", closes[0].reason, LoserSocketCloseReason)
	}
}

func TestNewRacerPreservesAllHeaderValues(t *testing.T) {
	headers := http.Header{"X-Test-Value": {"first", "second"}}
	fs := newRacer(waLog.Noop, http.DefaultClient, "ws://example.invalid", headers)
	headers["X-Test-Value"][0] = "changed"

	got := fs.HTTPHeaders.Values("X-Test-Value")
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("copied header values = %q, want [first second]", got)
	}
}

// The socket that connects first is the one kept.
func TestConnectRaceKeepsTheFirstToConnect(t *testing.T) {
	fast := newWSServer(t, 0)
	slow := newWSServer(t, 150*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fs, err := ConnectRace(ctx, waLog.Noop, http.DefaultClient,
		[]string{fast.wsURL(), slow.wsURL()}, http.Header{"Origin": {Origin}})
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	defer fs.Close(websocket.StatusNormalClosure)

	if fs.URL != fast.wsURL() {
		t.Fatalf("the slow endpoint won the race: %s", fs.URL)
	}
	if !fs.IsConnected() {
		t.Fatal("the winner is not connected")
	}
}

// With both upgrades completed before winner selection, exactly one
// client-visible socket survives and the opened loser is closed the client's
// way.
func TestConnectRaceLeavesOneSocket(t *testing.T) {
	a := newWSServer(t, 0)
	b := newWSServer(t, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Hold both racers after their upgrades complete. This forces winner
	// selection to happen with two opened sockets, so the loser must send the
	// explicit close frame rather than being an in-flight canceled dial.
	bothConnected := make(chan struct{})
	var connected atomic.Int32
	afterConnect := func(int) {
		if connected.Add(1) == 2 {
			close(bothConnected)
		}
		select {
		case <-bothConnected:
		case <-ctx.Done():
		}
	}
	fs, err := connectRace(ctx, waLog.Noop, http.DefaultClient, []string{a.wsURL(), b.wsURL()}, nil, afterConnect)
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	defer fs.Close(websocket.StatusNormalClosure)
	if !fs.IsConnected() {
		t.Fatal("the winner is not connected")
	}
	if err := fs.Context().Err(); err != nil {
		t.Fatalf("winner context was canceled with its completed dial: %v", err)
	}
	if fs.URL != a.wsURL() && fs.URL != b.wsURL() {
		t.Fatalf("the race returned an endpoint nobody offered: %s", fs.URL)
	}

	if connected.Load() != 2 {
		t.Fatalf("completed upgrades = %d, want 2", connected.Load())
	}
	loser := a
	if fs.URL == a.wsURL() {
		loser = b
	}
	rec := loser.awaitClose(t)[0]
	if rec.code != websocket.StatusNormalClosure || rec.reason != LoserSocketCloseReason {
		t.Fatalf("loser close = (%d, %q), want (%d, %q)",
			rec.code, rec.reason, websocket.StatusNormalClosure, LoserSocketCloseReason)
	}
}

// One URL is the old behaviour, unchanged.
func TestConnectRaceWithOneURL(t *testing.T) {
	only := newWSServer(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fs, err := ConnectRace(ctx, waLog.Noop, http.DefaultClient, []string{only.wsURL()}, nil)
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	if fs.URL != only.wsURL() || !fs.IsConnected() {
		t.Fatal("the single-URL path did not return a connected socket")
	}
	fs.Close(websocket.StatusNormalClosure)
}

// Every endpoint failing fails the call, and the errors are all reported.
func TestConnectRaceAllFail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ConnectRace(ctx, waLog.Noop, http.DefaultClient,
		[]string{"ws://127.0.0.1:1/a", "ws://127.0.0.1:1/b"}, nil)
	if err == nil {
		t.Fatal("a race where every endpoint failed returned a socket")
	}
}

// A racer that loses before it ever opened is aborted rather than left dialing.
func TestConnectRaceAbortsOutstandingDials(t *testing.T) {
	fast := newWSServer(t, 0)
	slow := newWSServer(t, 3*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	fs, err := ConnectRace(ctx, waLog.Noop, http.DefaultClient,
		[]string{fast.wsURL(), slow.wsURL()}, nil)
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	defer fs.Close(websocket.StatusNormalClosure)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the race waited %v for the slow endpoint", elapsed)
	}
}
