package whatsmeow

import (
	"context"
	"errors"
	"sync"

	"github.com/polymorfa/hypermeow/socket"
)

var (
	// ErrClientClosed means FinalClose permanently sealed this Client instance.
	ErrClientClosed = errors.New("client lifetime is closed")
	// ErrClientShutdownIncomplete means the tracked native subset joined, but
	// other client-owned async producers do not yet have a joined lifetime.
	// It is never confirmation of client termination or a deployment stop receipt.
	ErrClientShutdownIncomplete = errors.New("client shutdown coverage is incomplete")
	// ErrShutdownDeadlineRequired prevents an unbounded wait, including from a callback.
	ErrShutdownDeadlineRequired = errors.New("client shutdown requires a deadline")
)

type clientLifetime struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	sealed   bool
	active   int
	joined   chan struct{}
	stopped  chan struct{}
	sockets  map[*socket.FrameSocket]int
	contexts map[*nativeClientContext]struct{}
}

func (l *clientLifetime) initLocked() {
	if l.ctx == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
		l.joined = make(chan struct{})
		l.sockets = make(map[*socket.FrameSocket]int)
		l.contexts = make(map[*nativeClientContext]struct{})
	}
}

// Admission and completion share one lock. No Add can race a completed join.
func (cli *Client) enterNative() (func(), bool) {
	l := &cli.lifetime
	l.mu.Lock()
	l.initLocked()
	if l.sealed {
		l.mu.Unlock()
		return nil, false
	}
	l.active++
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		l.active--
		if l.sealed && l.active == 0 {
			close(l.joined)
		}
		l.mu.Unlock()
	}, true
}

func (cli *Client) startNative(fn func()) bool {
	done, ok := cli.enterNative()
	if !ok {
		return false
	}
	go func() { defer done(); fn() }()
	return true
}

// startConnectionTask owns a connection-event task and its forwarding context.
// Registration precedes scheduling even when final close wins before execution.
func (cli *Client) startConnectionTask(parent context.Context, fn func(context.Context)) bool {
	done, ok := cli.enterNative()
	if !ok {
		return false
	}
	ctx, release := cli.nativeContext(parent)
	go func() { defer done(); defer release(); fn(ctx) }()
	return true
}

func (cli *Client) dispatchEventAsync(evt any) bool {
	return cli.startNative(func() { cli.dispatchEvent(evt) })
}

func (cli *Client) nativeDone() <-chan struct{} {
	l := &cli.lifetime
	l.mu.Lock()
	defer l.mu.Unlock()
	l.initLocked()
	return l.ctx.Done()
}

func (cli *Client) nativeClosed() bool {
	l := &cli.lifetime
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sealed
}

// Socket workers include read/receive, disconnect callbacks, async close and
// writes that outlive SendFrame cancellation. Register before scheduling.
func (cli *Client) bindSocket(fs *socket.FrameSocket) func() {
	l := &cli.lifetime
	l.mu.Lock()
	l.initLocked()
	l.sockets[fs]++ // Keep the in-progress dial in the registry as well.
	l.mu.Unlock()
	release := func() {
		l.mu.Lock()
		l.sockets[fs]--
		if l.sockets[fs] == 0 && !fs.IsConnected() {
			delete(l.sockets, fs)
		}
		l.mu.Unlock()
	}
	fs.SetWorkerLauncher(func(fn func()) bool {
		done, ok := cli.enterNative()
		if !ok {
			return false
		}
		l.mu.Lock()
		l.sockets[fs]++
		l.mu.Unlock()
		go func() { defer done(); defer release(); fn() }()
		return true
	})
	return release
}

// nativeClientContext has explicit owners: a connect attempt, an admitted
// reconnect task, and (after a successful handshake) this client lifetime.
// Reconnect attempts reuse their inherited owner instead of growing a chain
// of cancellation watchers. Release never holds a client state lock.
type nativeClientContext struct {
	context.Context
	lifetime *clientLifetime
	mu       sync.Mutex
	refs     int
	cleanup  context.CancelFunc
}

func (ctx *nativeClientContext) retain() bool {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.refs == 0 {
		return false
	}
	ctx.refs++
	return true
}

func (ctx *nativeClientContext) release() {
	ctx.mu.Lock()
	ctx.refs--
	var cleanup context.CancelFunc
	if ctx.refs == 0 {
		cleanup = ctx.cleanup
		ctx.cleanup = nil
	}
	ctx.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

// Every attempt releases its owner on return. An admitted reconnect retains it
// until that task returns. An established socket pins it until final close:
// still-unbound async producers may inherit the event context after Disconnect,
// so cancelling it at ordinary socket/loop exit would change their behavior.
func (cli *Client) nativeContext(parent context.Context) (context.Context, context.CancelFunc) {
	l := &cli.lifetime
	if inherited, ok := parent.(*nativeClientContext); ok && inherited.lifetime == l && inherited.retain() {
		return inherited, inherited.release
	}
	l.mu.Lock()
	l.initLocked()
	lifetime := l.ctx
	l.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(lifetime, cancel)
	owned := &nativeClientContext{Context: ctx, lifetime: l, refs: 1, cleanup: func() { stop(); cancel() }}
	return owned, owned.release
}

// Keep ownership before admission/scheduling, and release even if admission is
// refused. This does not turn admission into a successful connection or join.
func (cli *Client) startNativeContext(ctx context.Context, fn func()) bool {
	release := func() {}
	if owned, ok := ctx.(*nativeClientContext); ok && owned.lifetime == &cli.lifetime {
		if !owned.retain() {
			return false
		}
		release = owned.release
	}
	if !cli.startNative(func() { defer release(); fn() }) {
		release()
		return false
	}
	return true
}

func (cli *Client) pinNativeContext(ctx context.Context) bool {
	owned, ok := ctx.(*nativeClientContext)
	if !ok || owned.lifetime != &cli.lifetime {
		return true
	} // Caller-owned internal contexts.
	l := &cli.lifetime
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return false
	}
	if _, exists := l.contexts[owned]; !exists {
		if !owned.retain() {
			return false
		}
		l.contexts[owned] = struct{}{}
	}
	return true
}

// FinalClose permanently seals this exact Client pointer. Unlike Disconnect,
// it forbids reconnect and new core worker/callback admissions. It starts one
// teardown shared by concurrent callers; cancelling a waiter does not retry or
// cancel teardown. Callers must provide a deadline, especially inside callbacks:
// a callback cannot finish joining its own invocation before it returns.
//
// This implementation joins the core socket/connect/reconnect/handler/event
// subset only. It ALWAYS returns ErrClientShutdownIncomplete after that join.
// See CLIENT_LIFETIME.md for unbound async producers. Public operations started
// by another runtime owner and work spawned by user callbacks are caller-owned.
// No logout, store deletion, assignment or authority change is performed.
func (cli *Client) FinalClose(ctx context.Context) error {
	if cli == nil {
		return ErrClientIsNil
	}
	if ctx == nil {
		return ErrShutdownDeadlineRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrShutdownDeadlineRequired
	}
	l := &cli.lifetime
	l.mu.Lock()
	l.initLocked()
	if !l.sealed {
		l.sealed = true
		l.stopped = make(chan struct{}) // Own teardown completion before scheduling.
		if l.active == 0 {
			close(l.joined)
		}
		go cli.finalDisconnect()
	}
	stopped := l.stopped
	l.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stopped:
		if err := ctx.Err(); err != nil {
			return err
		}
		return ErrClientShutdownIncomplete
	}
}

func (cli *Client) finalDisconnect() {
	// Cancellation is not a join. Keep even cancellation fanout outside the
	// lifetime mutex and the bounded caller.
	cli.lifetime.cancel()
	// Disconnect may wait for a dial/handshake's socket lock. Never make a
	// bounded caller perform that blocking work, or hold the lifetime lock here.
	cli.Disconnect()
	l := &cli.lifetime
	l.mu.Lock()
	sockets := make([]*socket.FrameSocket, 0, len(l.sockets))
	for fs := range l.sockets {
		sockets = append(sockets, fs)
	}
	l.mu.Unlock()
	for _, fs := range sockets {
		fs.Close(0)
	}
	<-l.joined
	l.mu.Lock()
	contexts := l.contexts
	l.contexts = nil
	l.mu.Unlock()
	for owned := range contexts {
		owned.release()
	}
	close(l.stopped)
}
