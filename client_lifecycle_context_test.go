package whatsmeow

import (
	"context"
	"errors"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type finalContextRefusal struct{}

func (finalContextRefusal) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("synthetic refusal")
}

type finalContextBarrierLog struct {
	waLog.Logger
	entered, release chan struct{}
}

func (l *finalContextBarrierLog) Errorf(msg string, args ...any) {
	if strings.HasPrefix(msg, "Initial connection failed but reconnecting") {
		close(l.entered)
		<-l.release
	}
}
func TestFinalCloseInitialReconnectSealRace(t *testing.T) {
	l := &finalContextBarrierLog{Logger: waLog.Noop, entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, l)
	c.InitialAutoReconnect = true
	c.SetPreLoginHTTPClient(&http.Client{Transport: finalContextRefusal{}})
	result := make(chan error, 1)
	go func() { result <- c.ConnectContext(context.Background()) }()
	select {
	case <-l.entered:
	case <-time.After(time.Second):
		t.Fatal("no reconnect log")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- c.FinalClose(ctx) }()
	select {
	case <-c.nativeDone():
	case <-time.After(time.Second):
		t.Fatal("not sealed")
	}
	close(l.release)
	err := <-result
	closeErr := <-stopped
	if !errors.Is(closeErr, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", closeErr)
	}
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("sealed reconnect admission must return ErrClientClosed: %v", err)
	}
}
func TestFinalCloseFailedAttemptsReleaseContextWatchers(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	c.SetPreLoginHTTPClient(&http.Client{Transport: finalContextRefusal{}})
	for i := 0; i < 100; i++ {
		if c.ConnectContext(context.Background()) == nil {
			t.Fatal("unexpected dial success")
		}
	}
	c.lifetime.mu.Lock()
	retained := reflect.ValueOf(c.lifetime.ctx).Elem().FieldByName("children").Len()
	c.lifetime.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.FinalClose(ctx); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("cleanup: %v", err)
	}
	if retained != 0 {
		t.Fatalf("100 completed failed dials retained %d lifetime cancellation watchers before FinalClose", retained)
	}
}

// Inspect only quiescent forwarding registrations on the pinned Go toolchain.
// This is the R1 independent observation, not a replacement implementation.
func finalContextWatcherCount(c *Client) int {
	c.lifetime.mu.Lock()
	defer c.lifetime.mu.Unlock()
	return reflect.ValueOf(c.lifetime.ctx).Elem().FieldByName("children").Len()
}

func TestFinalCloseInternalFailedAndEarlyAttemptsReleaseContexts(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	c.SetPreLoginHTTPClient(&http.Client{Transport: finalContextRefusal{}})
	for i := 0; i < 100; i++ {
		if err := c.connect(context.Background()); err == nil {
			t.Fatal("unexpected internal dial success")
		}
	}
	// Store-deleted is an early return before socket creation. The same deferred
	// release also covers ErrAlreadyConnected; no live socket is fabricated here.
	c.Store.Deleted = true
	for i := 0; i < 100; i++ {
		if err := c.ConnectContext(context.Background()); !errors.Is(err, store.ErrDeviceDeleted) {
			t.Fatalf("early return: %v", err)
		}
	}
	if count := finalContextWatcherCount(c); count != 0 {
		t.Fatalf("completed internal/early attempts retained %d watchers", count)
	}
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
}

// This uses the same ownership-transfer seam as a completed handshake, without
// claiming any handshake, live socket or provider acceptance.
func TestFinalCloseEstablishedContextSurvivesAttemptAndDisconnect(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	ctx, releaseAttempt := c.nativeContext(context.Background())
	if !c.pinNativeContext(ctx) {
		t.Fatal("established ownership refused")
	}
	releaseAttempt()
	if ctx.Err() != nil {
		t.Fatalf("attempt return cancelled established context: %v", ctx.Err())
	}
	c.Disconnect()
	if ctx.Err() != nil {
		t.Fatal("ordinary Disconnect cancelled inherited unbound event work")
	}
	if count := finalContextWatcherCount(c); count != 1 {
		t.Fatalf("established lifetime must own exactly one watcher, got %d", count)
	}
	// Every inherited reconnect attempt reuses the scope, without a nested
	// forwarding watcher or a cancellation of the established parent.
	for i := 0; i < 100; i++ {
		attempt, release := c.nativeContext(ctx)
		if attempt != ctx {
			t.Fatal("inherited reconnect allocated a nested context")
		}
		release()
	}
	if ctx.Err() != nil || finalContextWatcherCount(c) != 1 {
		t.Fatal("reconnect attempt changed established context lifetime")
	}
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("final close did not cancel established context: %v", ctx.Err())
	}
	if count := finalContextWatcherCount(c); count != 0 {
		t.Fatalf("final close retained %d watchers", count)
	}
}

type finalContextReconnectTransport struct {
	calls   atomic.Int32
	entered chan context.Context
}

func (rt *finalContextReconnectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.calls.Add(1) == 1 {
		return nil, errors.New("synthetic initial refusal")
	}
	rt.entered <- req.Context()
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestFinalCloseAdmittedReconnectOwnsContextAfterInitialReturn(t *testing.T) {
	id := types.NewJID("synthetic", types.DefaultUserServer)
	c := NewClient(&store.Device{ID: &id}, nil)
	c.InitialAutoReconnect = true
	rt := &finalContextReconnectTransport{entered: make(chan context.Context, 1)}
	c.SetWebsocketHTTPClient(&http.Client{Transport: rt})
	if err := c.ConnectContext(context.Background()); err != nil {
		t.Fatalf("initial reconnect admission: %v", err)
	}
	var reconnectCtx context.Context
	select {
	case reconnectCtx = <-rt.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("admitted reconnect lost its context before dialing")
	}
	if err := reconnectCtx.Err(); err != nil {
		t.Fatalf("attempt return cancelled admitted reconnect: %v", err)
	}
	if count := finalContextWatcherCount(c); count != 1 {
		t.Fatalf("reconnect must reuse one forwarding scope, got %d watchers", count)
	}
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	if rt.calls.Load() != 2 {
		t.Fatalf("unexpected late/repeated dial count: %d", rt.calls.Load())
	}
	if count := finalContextWatcherCount(c); count != 0 {
		t.Fatalf("cancelled reconnect retained %d watchers", count)
	}
}

type finalContextRecordingRefusal struct {
	first chan context.Context
	calls atomic.Int32
}

func (rt *finalContextRecordingRefusal) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.calls.Add(1) == 1 {
		rt.first <- req.Context()
	}
	return nil, errors.New("synthetic refusal")
}
func TestFinalCloseStoppedReconnectReleasesContextWithoutFinalClose(t *testing.T) {
	id := types.NewJID("synthetic", types.DefaultUserServer)
	c := NewClient(&store.Device{ID: &id}, nil)
	c.InitialAutoReconnect = true
	c.AutoReconnectHook = func(error) bool { return false }
	rt := &finalContextRecordingRefusal{first: make(chan context.Context, 1)}
	c.SetWebsocketHTTPClient(&http.Client{Transport: rt})
	if err := c.ConnectContext(context.Background()); err != nil {
		t.Fatalf("initial admission: %v", err)
	}
	inherited := <-rt.first
	select {
	case <-inherited.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("ended reconnect retained its forwarding context")
	}
	if count := finalContextWatcherCount(c); count != 0 {
		t.Fatalf("ended reconnect retained %d watchers before FinalClose", count)
	}
	if rt.calls.Load() != 2 {
		t.Fatalf("unexpected dial count: %d", rt.calls.Load())
	}
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
}

func TestFinalCloseRefusedContextLaunchReleasesOnlyItsReservation(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	ctx, release := c.nativeContext(context.Background())
	// Model the attempt's outstanding owner while a final seal wins admission.
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	if c.startNativeContext(ctx, func() { t.Error("late reconnect ran") }) {
		t.Fatal("late context worker admitted")
	}
	release()
	if count := finalContextWatcherCount(c); count != 0 {
		t.Fatalf("refused launch retained %d watchers", count)
	}
}
