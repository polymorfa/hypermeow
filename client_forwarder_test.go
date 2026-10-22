package whatsmeow

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/store"
)

// WithCancel uses this parent's AfterFunc registration. The derived context's
// first real cancel reaches this blocking unregister after closing derived Done.
// A concurrent second cancel sees the already-canceled child and can return.
// This is a synthetic custom Context boundary, not a substituted CancelFunc.
type forwarderBlockingParent struct {
	context.Context
	never                 chan struct{}
	entered, release      chan struct{}
	stopOnce, releaseOnce sync.Once
}

func (p *forwarderBlockingParent) Done() <-chan struct{} { return p.never }
func (p *forwarderBlockingParent) AfterFunc(func()) func() bool {
	return func() bool {
		stopped := false
		p.stopOnce.Do(func() { stopped = true; close(p.entered); <-p.release })
		return stopped
	}
}
func (p *forwarderBlockingParent) unblock() { p.releaseOnce.Do(func() { close(p.release) }) }

func forwarderWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("forwarder barrier not reached")
	}
}
func forwarderClose(t *testing.T, c *Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.FinalClose(ctx); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Errorf("forwarder cleanup: %v", err)
	}
}

func TestForwarderActualCancelReturnJoinsFinalTeardown(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	p := &forwarderBlockingParent{Context: context.Background(), never: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
	var releaseContext, finishOwner func()
	var releaseOnce, finishOnce sync.Once
	release := func() {
		if releaseContext != nil {
			releaseOnce.Do(releaseContext)
		}
	}
	finish := func() {
		if finishOwner != nil {
			finishOnce.Do(finishOwner)
		}
	}
	// Install all releases before admission, scheduling or any fatal assertion.
	t.Cleanup(func() { p.unblock(); release(); finish(); forwarderClose(t, c) })
	var ok bool
	finishOwner, ok = c.enterNative()
	if !ok {
		t.Fatal("initial reservation refused")
	}
	var inherited context.Context
	inherited, releaseContext = c.nativeContext(p)
	waiter, cancelWaiter := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelWaiter()
	result := make(chan error, 1)
	go func() { result <- c.FinalClose(waiter) }()
	forwarderWait(t, p.entered)
	forwarderWait(t, inherited.Done())
	if !errors.Is(inherited.Err(), context.Canceled) {
		t.Fatal("actual derived cancellation missing")
	}
	released := make(chan struct{})
	go func() { release(); close(released) }()
	forwarderWait(t, released) // Ordinary last-reference cleanup must not join the first cancel.
	finish()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("final teardown completed before forwarding cancel returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bounded final waiter hung")
	}
	p.unblock()
	forwarderClose(t, c)
}

// Done blocks construction before the registration, after startConnectionTask
// reserved its actual owner. Its values and ordinary cancellation stay intact.
type forwarderConstructionParent struct {
	context.Context
	entered, release  chan struct{}
	once, releaseOnce sync.Once
}

func (p *forwarderConstructionParent) Done() <-chan struct{} {
	p.once.Do(func() { close(p.entered); <-p.release })
	return p.Context.Done()
}
func (p *forwarderConstructionParent) unblock() { p.releaseOnce.Do(func() { close(p.release) }) }

type forwarderValueKey struct{}

func TestForwarderRegistrationAfterSealHasReservedTaskOwner(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	p := &forwarderConstructionParent{Context: context.WithValue(context.Background(), forwarderValueKey{}, "preserved"), entered: make(chan struct{}), release: make(chan struct{})}
	taskRelease := make(chan struct{})
	var taskOnce sync.Once
	unblockTask := func() { taskOnce.Do(func() { close(taskRelease) }) }
	t.Cleanup(func() { p.unblock(); unblockTask(); forwarderClose(t, c) })
	inherited := make(chan context.Context, 1)
	launched := make(chan bool, 1)
	go func() {
		launched <- c.startConnectionTask(p, func(ctx context.Context) { inherited <- ctx; <-taskRelease })
	}()
	forwarderWait(t, p.entered)
	waiter, cancel := context.WithCancel(finalTestContext(t))
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- c.FinalClose(waiter) }()
	forwarderWait(t, c.nativeDone()) // Establish cancellation-before-registration, not derived Err ordering.
	if !c.nativeClosed() {
		t.Fatal("constructor barrier did not follow final seal")
	}
	p.unblock()
	var ctx context.Context
	select {
	case ctx = <-inherited:
	case <-time.After(2 * time.Second):
		t.Fatal("reserved task did not run")
	}
	if !<-launched {
		t.Fatal("already reserved task was refused")
	}
	forwarderWait(t, ctx.Done())
	if !errors.Is(ctx.Err(), context.Canceled) || ctx.Value(forwarderValueKey{}) != "preserved" {
		t.Fatal("late registration lost cancellation or values")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked task waiter: %v", err)
	}
	c.lifetime.mu.Lock()
	active := c.lifetime.active
	c.lifetime.mu.Unlock()
	if active != 1 {
		t.Fatalf("reserved task not retained: %d", active)
	}
	unblockTask()
	forwarderClose(t, c)
	if forwarderCount(c) != 0 {
		t.Fatal("late registration retained completed entry")
	}
}

func forwarderCount(c *Client) int {
	c.lifetime.mu.Lock()
	defer c.lifetime.mu.Unlock()
	return len(c.lifetime.forwarders)
}
func forwarderEntry(c *Client) *nativeForwarder {
	c.lifetime.mu.Lock()
	defer c.lifetime.mu.Unlock()
	for entry := range c.lifetime.forwarders {
		return entry
	}
	return nil
}

func TestForwarderStopAndPublicationStates(t *testing.T) {
	t.Run("stop-before-start-concurrent", func(t *testing.T) {
		c := NewClient(&store.Device{}, nil)
		var finish func()
		var finishOnce sync.Once
		t.Cleanup(func() {
			if finish != nil {
				finishOnce.Do(finish)
			}
			forwarderClose(t, c)
		})
		var ok bool
		finish, ok = c.enterNative()
		if !ok {
			t.Fatal("reservation refused")
		}
		var calls atomic.Int32
		stop := c.lifetime.registerForwarder(c.lifetime.ctx, func() { calls.Add(1) })
		entry := forwarderEntry(c)
		if entry == nil {
			t.Fatal("entry not registered before scheduling")
		}
		stopped := make(chan bool, 4)
		for i := 0; i < 4; i++ {
			go func() { stopped <- stop() }()
		}
		winners := 0
		for i := 0; i < 4; i++ {
			if <-stopped {
				winners++
			}
		}
		forwarderWait(t, entry.done)
		if winners != 1 || calls.Load() != 0 || forwarderCount(c) != 0 {
			t.Fatalf("stop ownership: winners%d calls%d entries%d", winners, calls.Load(), forwarderCount(c))
		}
		finishOnce.Do(finish)
		forwarderClose(t, c)
		if calls.Load() != 0 || stop() {
			t.Fatal("stopped registration ran or resurrected")
		}
	})
	t.Run("already-completed", func(t *testing.T) {
		c := NewClient(&store.Device{}, nil)
		var finish func()
		var finishOnce sync.Once
		t.Cleanup(func() {
			if finish != nil {
				finishOnce.Do(finish)
			}
			forwarderClose(t, c)
		})
		var ok bool
		finish, ok = c.enterNative()
		if !ok {
			t.Fatal("reservation refused")
		}
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		stop := c.lifetime.registerForwarder(parent, func() { calls.Add(1) })
		entry := forwarderEntry(c)
		if entry == nil {
			t.Fatal("entry missing")
		}
		cancel()
		forwarderWait(t, entry.done)
		if stop() || calls.Load() != 1 || forwarderCount(c) != 0 {
			t.Fatal("completed callback stop/retirement changed")
		}
		finishOnce.Do(finish)
	})
	t.Run("callback-before-stop-publication", func(t *testing.T) {
		c := NewClient(&store.Device{}, nil)
		var finish func()
		var finishOnce sync.Once
		t.Cleanup(func() {
			if finish != nil {
				finishOnce.Do(finish)
			}
			forwarderClose(t, c)
		})
		var ok bool
		finish, ok = c.enterNative()
		if !ok {
			t.Fatal("reservation refused")
		}
		parent, cancel := context.WithCancel(context.Background())
		cancel()
		var calls atomic.Int32
		published := false
		stop := c.lifetime.registerForwarderUsing(parent, func() { calls.Add(1) }, func(ctx context.Context, fn func()) func() bool {
			// Hold only registrar result publication. Real stdlib AfterFunc still owns
			// asynchronous scheduling; the callback body and retirement are production.
			entry := forwarderEntry(c)
			if entry == nil {
				t.Fatal("registration not published before AfterFunc")
			}
			underlying := context.AfterFunc(ctx, fn)
			forwarderWait(t, entry.done)
			if forwarderCount(c) != 0 || calls.Load() != 1 {
				t.Fatal("callback depended on stop publication")
			}
			published = true
			return underlying
		})
		if !published || stop() || forwarderCount(c) != 0 {
			t.Fatal("late stop publication resurrected entry")
		}
		finishOnce.Do(finish)
	})
}

func TestForwarderPinnedReuseAndForeignLifetimeIsolation(t *testing.T) {
	c, foreign := NewClient(&store.Device{}, nil), NewClient(&store.Device{}, nil)
	var releaseAttempt, finishAttempt, releaseForeign, finishForeign func()
	var attemptOnce, ownerOnce, foreignOnce, foreignOwnerOnce sync.Once
	releaseA := func() {
		if releaseAttempt != nil {
			attemptOnce.Do(releaseAttempt)
		}
	}
	finishA := func() {
		if finishAttempt != nil {
			ownerOnce.Do(finishAttempt)
		}
	}
	releaseB := func() {
		if releaseForeign != nil {
			foreignOnce.Do(releaseForeign)
		}
	}
	finishB := func() {
		if finishForeign != nil {
			foreignOwnerOnce.Do(finishForeign)
		}
	}
	t.Cleanup(func() {
		releaseB()
		finishB()
		releaseA()
		finishA()
		forwarderClose(t, foreign)
		forwarderClose(t, c)
	})
	var ok bool
	finishAttempt, ok = c.enterNative()
	if !ok {
		t.Fatal("attempt reservation refused")
	}
	parent := context.WithValue(context.Background(), forwarderValueKey{}, "retained")
	var ctx context.Context
	ctx, releaseAttempt = c.nativeContext(parent)
	if !c.pinNativeContext(ctx) {
		t.Fatal("pin refused")
	}
	releaseA()
	finishA()
	c.Disconnect()
	if ctx.Err() != nil || forwarderCount(c) != 1 {
		t.Fatal("ordinary release/disconnect canceled successful scope")
	}
	// Model two simultaneous inherited attempt owners, rather than another new
	// successful plain-caller scope. Both must retain/reuse the exact pinned scope.
	done, ok := c.enterNative()
	if !ok {
		t.Fatal("reconnect reservation refused")
	}
	var doneOnce sync.Once
	t.Cleanup(func() { doneOnce.Do(done) })
	a, release1 := c.nativeContext(ctx)
	b, release2 := c.nativeContext(ctx)
	var r1, r2 sync.Once
	t.Cleanup(func() { r1.Do(release1); r2.Do(release2) })
	if a != ctx || b != ctx || forwarderCount(c) != 1 {
		t.Fatal("inherited attempts allocated another registration")
	}
	returned := make(chan struct{}, 2)
	go func() { r1.Do(release1); returned <- struct{}{} }()
	go func() { r2.Do(release2); returned <- struct{}{} }()
	<-returned
	<-returned
	doneOnce.Do(done)
	if ctx.Err() != nil {
		t.Fatal("inherited attempt release canceled pin")
	}
	finishForeign, ok = foreign.enterNative()
	if !ok {
		t.Fatal("foreign reservation refused")
	}
	var other context.Context
	other, releaseForeign = foreign.nativeContext(ctx)
	if other == ctx || !foreign.pinNativeContext(other) {
		t.Fatal("foreign client reused exact owner or pin refused")
	}
	releaseB()
	finishB()
	forwarderClose(t, foreign)
	forwarderWait(t, other.Done())
	if !errors.Is(other.Err(), context.Canceled) || ctx.Err() != nil || other.Value(forwarderValueKey{}) != "retained" {
		t.Fatal("foreign cancellation escaped its derived scope")
	}
	forwarderClose(t, c)
	forwarderWait(t, ctx.Done())
	if !errors.Is(ctx.Err(), context.Canceled) || forwarderCount(c) != 0 || forwarderCount(foreign) != 0 {
		t.Fatal("pin/registration cleanup did not join")
	}
	c.lifetime.mu.Lock()
	active, pins := c.lifetime.active, len(c.lifetime.contexts)
	c.lifetime.mu.Unlock()
	if active != 0 || pins != 0 {
		t.Fatalf("pin/active cycle or retention: %d/%d", active, pins)
	}
}

func TestForwarderReentrantAndConcurrentFinalWaiters(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	var finish func()
	var finishOnce sync.Once
	t.Cleanup(func() {
		if finish != nil {
			finishOnce.Do(finish)
		}
		forwarderClose(t, c)
	})
	var ok bool
	finish, ok = c.enterNative()
	if !ok {
		t.Fatal("reservation refused")
	}
	entered := make(chan struct{})
	reentry := make(chan error, 1)
	c.lifetime.registerForwarder(c.lifetime.ctx, func() {
		close(entered)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		reentry <- c.FinalClose(ctx)
	})
	finishOnce.Do(finish)
	waiter, cancel := context.WithCancel(finalTestContext(t))
	defer cancel()
	canceled := make(chan error, 1)
	go func() { canceled <- c.FinalClose(waiter) }()
	forwarderWait(t, entered)
	c.lifetime.mu.Lock()
	originalStop := c.lifetime.stopped
	c.lifetime.mu.Unlock()
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: %v", err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		ctx := finalTestContext(t)
		go func() { results <- c.FinalClose(ctx) }()
	}
	if err := <-reentry; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("self-forwarder wait confirmed completion: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Fatalf("coalesced result: %v", err)
		}
	}
	forwarderClose(t, c)
	c.lifetime.mu.Lock()
	same := c.lifetime.stopped == originalStop
	c.lifetime.mu.Unlock()
	if !same || forwarderCount(c) != 0 {
		t.Fatal("waiter cancellation retried teardown or retained callback")
	}
	replacement := NewClient(&store.Device{}, nil)
	t.Cleanup(func() { forwarderClose(t, replacement) })
	ran := make(chan struct{})
	if !replacement.startConnectionTask(context.Background(), func(ctx context.Context) {
		if ctx.Err() != nil {
			t.Error("replacement canceled")
		}
		close(ran)
	}) {
		t.Fatal("old final close sealed replacement")
	}
	forwarderWait(t, ran)
}
