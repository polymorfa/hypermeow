package whatsmeow

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/appstate"
	"github.com/polymorfa/hypermeow/proto/waServerSync"
	"github.com/polymorfa/hypermeow/store"
)

type finalAppStateKeys struct {
	store.NoopStore
	calls   atomic.Int32
	entered chan context.Context
	release chan struct{}
}

func (s *finalAppStateKeys) GetAppStateSyncKey(ctx context.Context, _ []byte) (*store.AppStateSyncKey, error) {
	if s.calls.Add(1) > 1 {
		s.entered <- ctx
		<-s.release
	}
	return nil, nil
}
func finalAppStatePatches() *appstate.PatchList {
	return &appstate.PatchList{Snapshot: &waServerSync.SyncdSnapshot{KeyID: &waServerSync.KeyId{ID: []byte("synthetic-key")}}}
}
func TestFinalAppStateMissingKeyActualStoreJoin(t *testing.T) {
	keys := &finalAppStateKeys{entered: make(chan context.Context, 1), release: make(chan struct{})}
	c := NewClient(&store.Device{AppStateKeys: keys}, nil)
	var once sync.Once
	release := func() { once.Do(func() { close(keys.release) }) }
	t.Cleanup(func() {
		release()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), finalConnectionContextKey{}, "appstate-value"))
	defer cancel()
	_, err := c.applyAppStatePatches(parent, appstate.WAPatchRegular, appstate.HashState{}, finalAppStatePatches(), false, nil)
	if !errors.Is(err, appstate.ErrKeyNotFound) {
		t.Fatalf("unexpected missing-key result: %v", err)
	}
	var ctx context.Context
	select {
	case ctx = <-keys.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("missing-key scan store not reached")
	}
	cancel()
	c.Disconnect()
	if ctx.Err() != nil || ctx.Value(finalConnectionContextKey{}) != "appstate-value" {
		t.Fatal("WithoutCancel ancestry or values changed")
	}
	finalTestBlocked(t, c)
	finalTestWait(t, ctx.Done())
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("final cancellation not forwarded")
	}
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
}

func TestFinalAppStateMissingKeyRefusalDoesNotTouchBody(t *testing.T) {
	for _, seeded := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil-cache", true: "seeded-cache"}[seeded], func(t *testing.T) {
			keys := &finalAppStateKeys{entered: make(chan context.Context, 1), release: make(chan struct{})}
			c := NewClient(&store.Device{AppStateKeys: keys}, nil)
			var once sync.Once
			release := func() { once.Do(func() { close(keys.release) }) }
			t.Cleanup(func() {
				release()
				if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
					t.Error(err)
				}
			})
			stamp := time.Unix(123, 456)
			if seeded {
				c.appStateKeyRequests = map[string]time.Time{"old": stamp}
			}
			if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
				t.Fatal(err)
			}
			if c.startMissingAppStateKeyRequest(context.WithoutCancel(context.Background()), finalAppStatePatches()) {
				t.Fatal("sealed key request admitted")
			}
			if keys.calls.Load() != 0 {
				t.Fatal("refused task ran GetMissingKeyIDs/store")
			}
			if seeded {
				if len(c.appStateKeyRequests) != 1 || c.appStateKeyRequests["old"] != stamp {
					t.Fatal("refusal changed request cache/time")
				}
			} else if c.appStateKeyRequests != nil {
				t.Fatal("refusal initialized request cache")
			}
			if finalContextWatcherCount(c) != 0 {
				t.Fatal("refusal retained forwarding owner")
			}
		})
	}
}

func TestFinalAppStateBatchMembersJoinAcrossSealAndReentry(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	first, second := make(chan struct{}), make(chan struct{})
	releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
	reentering := make(chan struct{})
	selfResult := make(chan error, 1)
	waiter, cancel := context.WithCancel(finalTestContext(t))
	defer cancel()
	var once1, once2 sync.Once
	unblock1 := func() { once1.Do(func() { close(releaseFirst) }) }
	unblock2 := func() { once2.Do(func() { close(releaseSecond) }) }
	t.Cleanup(func() {
		cancel()
		unblock1()
		unblock2()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	var mu sync.Mutex
	var order []string
	var late atomic.Bool
	record := func(v string) { mu.Lock(); order = append(order, v); mu.Unlock() }
	c.AddEventHandler(func(evt any) {
		member := evt.(string)
		record("A" + member)
		if member == "1" {
			close(first)
			<-releaseFirst
			if c.startAppStateEventBatch(func(func(any) bool) { late.Store(true) }) {
				late.Store(true)
			}
			close(reentering)
			selfResult <- c.FinalClose(waiter)
		} else if member == "2" {
			close(second)
			<-releaseSecond
		}
	})
	c.AddEventHandler(func(evt any) { record("B" + evt.(string)) })
	eventsToDispatch := []any{"1", "2"}
	if !c.startAppStateEventBatch(func(dispatch func(any) bool) {
		for _, evt := range eventsToDispatch {
			dispatch(evt)
		}
	}) {
		t.Fatal("batch refused")
	}
	finalTestWait(t, first)
	c.Disconnect()
	if c.nativeClosed() {
		t.Fatal("ordinary Disconnect sealed client")
	}
	// The range header is already captured as in the original anonymous loop.
	eventsToDispatch = []any{"changed"}
	finalTestBlocked(t, c)
	unblock1()
	finalTestWait(t, reentering)
	cancel()
	select {
	case err := <-selfResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("self waiter: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant FinalClose stuck")
	}
	finalTestWait(t, second)
	finalTestBlocked(t, c) // The later member is still an actual held callback.
	unblock2()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"A1", "B1", "A2", "B2"}) {
		t.Fatalf("batch truncated or reordered: %v", got)
	}
	if late.Load() {
		t.Fatal("reentrant post-seal batch admitted")
	}
}
func TestFinalAppStateBatchRefusalAndOrdinaryReuse(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	t.Cleanup(func() {
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	var calls atomic.Int32
	done := make(chan struct{}, 2)
	c.AddEventHandler(func(any) { calls.Add(1); done <- struct{}{} })
	launch := func() bool { return c.startAppStateEventBatch(func(dispatch func(any) bool) { dispatch("synthetic") }) }
	for i := 0; i < 2; i++ {
		c.Disconnect()
		if !launch() {
			t.Fatal("ordinary reusable client refused batch")
		}
		finalTestWait(t, done)
	}
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	if launch() {
		t.Fatal("post-seal batch admitted")
	}
	if calls.Load() != 2 {
		t.Fatal("callback count changed on refusal")
	}
}
func TestFinalAppStateBatchFailureAndPanicPreserveMemberOrder(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	t.Cleanup(func() {
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	var mu sync.Mutex
	var order []string
	record := func(v string) { mu.Lock(); order = append(order, v); mu.Unlock() }
	c.AddEventHandlerWithSuccessStatus(func(evt any) bool {
		member := evt.(string)
		record("A" + member)
		if member == "1" {
			return false
		}
		if member == "2" {
			panic("synthetic callback panic")
		}
		return true
	})
	c.AddEventHandler(func(evt any) { record("B" + evt.(string)) })
	batchReturned := make(chan struct{})
	if !c.startAppStateEventBatch(func(dispatch func(any) bool) {
		defer close(batchReturned)
		for _, evt := range []any{"1", "2", "3"} {
			dispatch(evt)
		}
	}) {
		t.Fatal("batch refused")
	}
	finalTestWait(t, batchReturned)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"A1", "A2", "A3", "B3"}) {
		t.Fatalf("failure/panic semantics changed: %v", got)
	}
}
