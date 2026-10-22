package whatsmeow

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/store"
)

func TestFinalCloseSealsExactClient(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cli.FinalClose(ctx); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("want incomplete coverage, got %v", err)
	}
	if err := cli.ConnectContext(ctx); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("connect after close: %v", err)
	}
	if err := cli.FinalClose(ctx); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("replay: %v", err)
	}
}

func finalTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func finalTestWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("test worker did not reach barrier")
	}
}

func finalTestBlocked(t *testing.T, cli *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sealed := cli.nativeDone()
	result := make(chan error, 1)
	go func() { result <- cli.FinalClose(ctx) }()
	finalTestWait(t, sealed)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked lifetime must cancel, got %v", err)
	}
	cli.lifetime.mu.Lock()
	stopped := cli.lifetime.stopped
	cli.lifetime.mu.Unlock()
	select {
	case <-stopped:
		t.Fatal("reported teardown completion while native work is blocked")
	default:
	}
}

func TestFinalCloseWaitsCallbackAfterDisconnect(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	cli.AddEventHandler(func(any) { close(entered); <-release })
	go func() { cli.dispatchEvent("synthetic event"); close(returned) }()
	finalTestWait(t, entered)
	cli.Disconnect() // A disconnected socket does not complete the admitted callback.
	finalTestBlocked(t, cli)
	if cli.startNative(func() { t.Error("late worker ran") }) {
		t.Fatal("late admission accepted")
	}
	if !cli.dispatchEvent("late event") {
		t.Fatal("late callback admission accepted")
	}
	close(release)
	finalTestWait(t, returned)
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("join must retain coverage refusal: %v", err)
	}
}

func TestFinalCloseWaitsHandlerAfterLoopCancellation(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	entered, release, loopDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	cli.nodeHandlers["lifetime_test"] = func(context.Context, *waBinary.Node) { close(entered); <-release }
	connCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cli.startNative(func() { defer close(loopDone); cli.handlerQueueLoop(context.Background(), connCtx) })
	cli.handlerQueue <- &waBinary.Node{Tag: "lifetime_test"}
	finalTestWait(t, entered)
	cancel()
	finalTestBlocked(t, cli)
	finalTestWait(t, loopDone)
	close(release)
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("handler join: %v", err)
	}
}

func TestFinalCloseConcurrentCancellationReplayAndReplacement(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	cli.startNative(func() { close(entered); <-release })
	finalTestWait(t, entered)
	finalTestBlocked(t, cli)
	cli.lifetime.mu.Lock()
	originalStop := cli.lifetime.stopped
	cli.lifetime.mu.Unlock()
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { results <- cli.FinalClose(finalTestContext(t)) }()
	}
	cancelled, cancel := context.WithCancel(finalTestContext(t))
	cancel()
	if err := cli.FinalClose(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	replacement := NewClient(&store.Device{}, nil)
	replacementDone := make(chan struct{})
	if !replacement.startNative(func() { close(replacementDone) }) {
		t.Fatal("old stop sealed replacement")
	}
	finalTestWait(t, replacementDone)
	close(release)
	for i := 0; i < 4; i++ {
		if err := <-results; !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Fatalf("coalesced stop: %v", err)
		}
	}
	cli.lifetime.mu.Lock()
	sameStop := cli.lifetime.stopped == originalStop
	cli.lifetime.mu.Unlock()
	if !sameStop {
		t.Fatal("stop retried teardown")
	}
	if replacement.nativeClosed() {
		t.Fatal("replacement was stopped")
	}
}

func TestFinalCloseSelfCallbackRequiresOwnReturn(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	result := make(chan error, 1)
	cli.AddEventHandler(func(any) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		result <- cli.FinalClose(ctx)
	})
	cli.startNative(func() { cli.dispatchEvent("self stop") })
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("self callback must not confirm join: %v", err)
	}
	if !cli.nativeClosed() {
		t.Fatal("self-callback deadline expired before shutdown was initiated")
	}
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("outside callback: %v", err)
	}
}

func TestFinalCloseRejectsUnboundedAndCancelledBeforeStop(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	if err := cli.FinalClose(context.Background()); !errors.Is(err, ErrShutdownDeadlineRequired) {
		t.Fatalf("unbounded: %v", err)
	}
	ctx, cancel := context.WithCancel(finalTestContext(t))
	cancel()
	if err := cli.FinalClose(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if cli.nativeClosed() {
		t.Fatal("refused invocation sealed client")
	}
	cli.Disconnect()
	called := false
	cli.AddEventHandler(func(any) { called = true })
	cli.dispatchEvent("ordinary disconnect remains reusable")
	if !called {
		t.Fatal("Disconnect sealed normal callback admission")
	}
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
}

type finalCloseTransport struct {
	entered chan struct{}
	calls   atomic.Int32
}

func (rt *finalCloseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	close(rt.entered)
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestFinalCloseCancelsConnectAndPreventsReconnect(t *testing.T) {
	id := types.NewJID("synthetic", types.DefaultUserServer)
	cli := NewClient(&store.Device{ID: &id}, nil)
	transport := &finalCloseTransport{entered: make(chan struct{})}
	cli.SetWebsocketHTTPClient(&http.Client{Transport: transport})
	cli.InitialAutoReconnect = true
	result := make(chan error, 1)
	go func() { result <- cli.ConnectContext(context.Background()) }()
	finalTestWait(t, transport.entered)
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("stop dial: %v", err)
	}
	if err := <-result; err == nil {
		t.Fatal("cancelled connect reported success")
	}
	if err := cli.ConnectContext(finalTestContext(t)); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("late connect: %v", err)
	}
	cli.autoReconnect(context.Background())
	cli.ResetConnection()
	if n := transport.calls.Load(); n != 1 {
		t.Fatalf("unexpected reconnect: %d dials", n)
	}
}

func TestFinalCloseWaitsAsyncCleanupAndReservedLaunch(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	// A reserved launch is outstanding even before the goroutine is scheduled.
	done, ok := cli.enterNative()
	if !ok {
		t.Fatal("initial admission refused")
	}
	finalTestBlocked(t, cli)
	cleanup := make(chan struct{})
	go func() { defer done(); close(cleanup) }()
	finalTestWait(t, cleanup)
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("async cleanup join: %v", err)
	}
}

func TestFinalCloseWaitsRawReceiveCallback(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	cli.RawNodeHandler = func(context.Context, *waBinary.Node) (*waBinary.Node, bool) {
		close(entered)
		<-release
		return nil, true
	}
	data, err := waBinary.Marshal(waBinary.Node{Tag: "ack"})
	if err != nil {
		t.Fatal(err)
	}
	go func() { cli.handleFrame(context.Background(), data); close(returned) }()
	finalTestWait(t, entered)
	cli.Disconnect()
	finalTestBlocked(t, cli)
	close(release)
	finalTestWait(t, returned)
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("receive join: %v", err)
	}
	// Must not call RawNodeHandler again (it would close entered twice).
	cli.handleFrame(context.Background(), data)
}

type finalStubbornTransport struct{ entered, release chan struct{} }

func (rt *finalStubbornTransport) RoundTrip(*http.Request) (*http.Response, error) {
	close(rt.entered)
	<-rt.release
	return nil, errors.New("synthetic dial refused")
}
func TestFinalCloseDeadlineWhileConnectOwnsSocketLock(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	rt := &finalStubbornTransport{entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-rt.release:
		default:
			close(rt.release)
		}
	}()
	cli.SetPreLoginHTTPClient(&http.Client{Transport: rt})
	returned := make(chan error, 1)
	go func() { returned <- cli.ConnectContext(context.Background()) }()
	finalTestWait(t, rt.entered)
	finalTestBlocked(t, cli)
	// Teardown is still blocked on socketLock; admission/refusal never waits for it.
	if err := cli.ConnectContext(finalTestContext(t)); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("late connect blocked or admitted: %v", err)
	}
	finalTestBlocked(t, cli)
	close(rt.release)
	if err := <-returned; !errors.Is(err, ErrClientClosed) {
		t.Fatalf("sealed in-flight connect: %v", err)
	}
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("final dial cleanup: %v", err)
	}
}

type finalRefusingTransport struct{ calls atomic.Int32 }

func (rt *finalRefusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	return nil, errors.New("synthetic refusal")
}
func TestFinalCloseOrdinaryDisconnectAllowsConnectAgain(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	rt := &finalRefusingTransport{}
	cli.SetPreLoginHTTPClient(&http.Client{Transport: rt})
	if err := cli.ConnectContext(finalTestContext(t)); err == nil || errors.Is(err, ErrClientClosed) {
		t.Fatalf("first dial: %v", err)
	}
	cli.Disconnect()
	if err := cli.ConnectContext(finalTestContext(t)); err == nil || errors.Is(err, ErrClientClosed) {
		t.Fatalf("second dial: %v", err)
	}
	if rt.calls.Load() != 2 {
		t.Fatal("ordinary Disconnect prevented next dial")
	}
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("cleanup: %v", err)
	}
}
