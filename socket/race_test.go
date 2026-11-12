// Copyright (c) 2026 Rajeh Taher
// Copyright (c) 2026 Polymorfa (Softium LLC)
//
// Licensed under the MIT License. See LICENSE-MIT for details.

//go:build !js

package socket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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

func TestConnectRaceHeadersReachServer(t *testing.T) {
	for _, origin := range [][]string{nil, {}} {
		for _, endpoints := range []int{1, 2} {
			t.Run(fmt.Sprintf("origin=%v/endpoints=%d", origin, endpoints), func(t *testing.T) {
				seen := make(chan http.Header, endpoints)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					seen <- r.Header.Clone()
					conn, err := websocket.Accept(w, r, nil)
					if err == nil {
						defer conn.CloseNow()
						_, _, _ = conn.Read(r.Context())
					}
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				urls := make([]string, endpoints)
				for i := range urls {
					urls[i] = "ws" + server.URL[len("http"):]
				}
				fs, err := ConnectRace(ctx, waLog.Noop, http.DefaultClient, urls,
					http.Header{"Origin": origin, "X-Test-Value": {"first", "second"}})
				if err != nil {
					t.Fatal(err)
				}
				defer fs.Close(websocket.StatusNormalClosure)
				headers := <-seen
				if got := headers.Values("Origin"); len(got) != 0 {
					t.Errorf("suppressed Origin reached server: %q", got)
				}
				if got := headers.Values("X-Test-Value"); !slices.Equal(got, []string{"first", "second"}) {
					t.Errorf("repeated header = %q", got)
				}
			})
		}
	}
}

func TestConnectRaceErrorsFollowEndpointOrder(t *testing.T) {
	secondFinished := make(chan struct{})
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-secondFinished:
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The first endpoint cannot fail until the second response was received.
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(r)
		if r.URL.Host == strings.TrimPrefix(second.URL, "http://") {
			close(secondFinished)
		}
		return resp, err
	})}
	_, err := ConnectRace(ctx, waLog.Noop, client,
		[]string{"ws" + first.URL[4:], "ws" + second.URL[4:]}, nil)
	var statusErr ErrWithStatusCode
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusForbidden {
		t.Fatalf("first status = %v, want 403", err)
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 || !errors.As(joined.Unwrap()[1], &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second endpoint error missing: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type pumpLogger struct {
	waLog.Logger
	started chan struct{}
	exited  chan struct{}
}

func (l *pumpLogger) Debugf(msg string, _ ...any) {
	switch msg {
	case "Frame websocket read pump starting %p":
		l.started <- struct{}{}
	case "Frame websocket read pump exiting %p":
		l.exited <- struct{}{}
	}
}

func TestConnectRaceReturnsBeforeLoserCloseAndStartsOnlyWinnerPump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseServers := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseServers()
	closes := make(chan closeRecord, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		// Send a complete pre-handshake frame. An unconsumed loser must not
		// retain a read pump, and closing the unconsumed winner must release it.
		_ = conn.Write(ctx, websocket.MessageBinary, []byte{0, 0, 1, 42})
		select {
		case <-release:
		case <-ctx.Done():
			return
		}
		_, _, err = conn.Read(ctx)
		var closeErr websocket.CloseError
		errors.As(err, &closeErr)
		closes <- closeRecord{websocket.CloseStatus(err), closeErr.Reason}
	}))
	defer server.Close()
	logger := &pumpLogger{Logger: waLog.Noop, started: make(chan struct{}, 2), exited: make(chan struct{}, 2)}
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
	type outcome struct {
		fs  *FrameSocket
		err error
	}
	returned := make(chan outcome, 1)
	url := "ws" + server.URL[4:]
	go func() {
		fs, err := connectRace(ctx, logger, http.DefaultClient, []string{url, url}, nil, afterConnect)
		returned <- outcome{fs, err}
	}()
	var winner *FrameSocket
	select {
	case result := <-returned:
		if result.err != nil {
			t.Fatal(result.err)
		}
		winner = result.fs
	case <-time.After(time.Second):
		releaseServers()
		t.Fatal("winner waited for an unacknowledged loser close")
	}
	defer winner.Close(0)
	select {
	case <-logger.started:
	case <-ctx.Done():
		t.Fatal("winner read pump did not start")
	}
	winner.Close(0)
	select {
	case <-logger.exited:
	case <-time.After(time.Second):
		t.Fatal("closing a socket with no frame consumer retained its pump")
	}
	releaseServers()
	var explicitLosers int
	for range 2 {
		select {
		case rec := <-closes:
			if rec.code == websocket.StatusNormalClosure && rec.reason == LoserSocketCloseReason {
				explicitLosers++
			}
		case <-ctx.Done():
			t.Fatal("server did not observe socket cleanup")
		}
	}
	if explicitLosers != 1 {
		t.Fatalf("explicit loser closes = %d, want 1", explicitLosers)
	}
	select {
	case <-logger.started:
		t.Fatal("a loser read pump started")
	default:
	}
}

func TestFrameDeliveryStopsOnCancellation(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		t.Run(fmt.Sprintf("fragmented=%v", fragmented), func(t *testing.T) {
			fs := NewFrameSocket(waLog.Noop, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fs.cancelCtx = ctx
			if fragmented {
				fs.processData([]byte{0, 0, 2, 42})
			}
			done := make(chan struct{})
			go func() {
				if fragmented {
					fs.processData([]byte{43})
				} else {
					fs.processData([]byte{0, 0, 1, 42})
				}
				close(done)
			}()
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("frame delivery did not stop after cancellation")
			}
		})
	}
}

func TestConnectRaceCancelsEveryPendingDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	returned := make(chan error, 1)
	go func() {
		_, err := ConnectRace(ctx, waLog.Noop, client, []string{"ws://first.invalid", "ws://second.invalid"}, nil)
		returned <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("a pending dial did not start")
		}
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrDialFailed) {
			t.Fatalf("canceled race error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending race did not return on cancellation")
	}
}
