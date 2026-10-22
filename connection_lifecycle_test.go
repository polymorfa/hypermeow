package whatsmeow

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types/events"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

type finalConnectionBarrierLog struct {
	waLog.Logger
	prefix           string
	entered, release chan struct{}
	once             sync.Once
}

func (l *finalConnectionBarrierLog) Sub(string) waLog.Logger { return l }
func (l *finalConnectionBarrierLog) Infof(msg string, args ...any) {
	if strings.HasPrefix(msg, l.prefix) {
		l.once.Do(func() { close(l.entered); <-l.release })
	}
}

func TestFinalConnectionEventLaunchLosesToSeal(t *testing.T) {
	log := &finalConnectionBarrierLog{Logger: waLog.Noop, prefix: "Got replaced stream error", entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	called := false
	c.AddEventHandler(func(any) { called = true })
	c.startNative(func() {
		c.handleStreamError(context.Background(), &waBinary.Node{Tag: "stream:error", Attrs: waBinary.Attrs{"code": "409"}, Content: []waBinary.Node{{Tag: "conflict", Attrs: waBinary.Attrs{"type": "replaced"}}}})
	})
	finalTestWait(t, log.entered)
	finalTestBlocked(t, c)
	close(log.release)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	if called {
		t.Fatal("late connection-event callback admitted")
	}
}

type finalConnectionPrekeys struct {
	store.NoopStore
	entered chan context.Context
	release chan struct{}
}

func (s *finalConnectionPrekeys) UploadedPreKeyCount(ctx context.Context) (int, error) {
	s.entered <- ctx
	<-s.release
	return 0, errors.New("synthetic prekey inventory refusal")
}

type finalConnectionContextKey struct{}

func TestFinalConnectionSuccessJoinsBlockedOwnedTask(t *testing.T) {
	blocking := &finalConnectionPrekeys{entered: make(chan context.Context, 1), release: make(chan struct{})}
	c := NewClient(&store.Device{PreKeys: blocking}, nil)
	c.paired.Store(true)
	c.lastTCTokenDBPrune = time.Now() // Do not launch the separately unbound prune.
	parent := context.WithValue(context.Background(), finalConnectionContextKey{}, "preserved-value")
	c.handleConnectSuccess(parent, &waBinary.Node{Tag: "success", Attrs: waBinary.Attrs{"t": "1"}})
	var inherited context.Context
	select {
	case inherited = <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("post-connect task was not launched")
	}
	if inherited.Value(finalConnectionContextKey{}) != "preserved-value" {
		t.Fatal("event context value lost")
	}
	finalTestBlocked(t, c)
	if !errors.Is(inherited.Err(), context.Canceled) {
		t.Fatal("post-connect task did not receive final cancellation")
	}
	close(blocking.release)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("post-connect join: %v", err)
	}
	if finalContextWatcherCount(c) != 0 {
		t.Fatal("post-connect forwarding ownership leaked")
	}
}

func TestFinalConnectionAsyncEventPreservesOrdinaryDispatch(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	received := make(chan any, 1)
	c.AddEventHandler(func(evt any) { received <- evt })
	c.Disconnect()
	c.handleIB(context.Background(), &waBinary.Node{Tag: "ib", Content: []waBinary.Node{{Tag: "downgrade_webclient"}}})
	select {
	case evt := <-received:
		if _, ok := evt.(*events.QRScannedWithoutMultidevice); !ok {
			t.Fatalf("event type changed: %T", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary async event was sealed")
	}
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
}

func TestFinalConnectionCleanupReconnectLaunchLosesToSeal(t *testing.T) {
	log := &finalConnectionBarrierLog{Logger: waLog.Noop, prefix: "Got 515 code, reconnecting", entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	rt := &finalRefusingTransport{}
	c.SetPreLoginHTTPClient(&http.Client{Transport: rt})
	c.startNative(func() {
		c.handleStreamError(context.Background(), &waBinary.Node{Tag: "stream:error", Attrs: waBinary.Attrs{"code": "515"}})
	})
	finalTestWait(t, log.entered)
	finalTestBlocked(t, c)
	close(log.release)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	if rt.calls.Load() != 0 {
		t.Fatal("sealed 515 cleanup admitted reconnect")
	}
}
