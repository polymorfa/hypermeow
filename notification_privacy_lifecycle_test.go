package whatsmeow

import (
	"context"
	"errors"
	waBinary "github.com/polymorfa/hypermeow/binary"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

type finalPrivacyStore struct {
	store.NoopStore
	entered chan context.Context
	release chan struct{}
}

func (s *finalPrivacyStore) DeleteExpiredPrivacyTokens(ctx context.Context, _ time.Time) (int64, error) {
	s.entered <- ctx
	<-s.release // Deliberately ignore cancellation: completion must still be joined.
	return 0, nil
}
func (s *finalPrivacyStore) GetPrivacyToken(context.Context, types.JID) (*store.PrivacyToken, error) {
	return nil, nil
}
func (s *finalPrivacyStore) PutPrivacyTokens(context.Context, ...store.PrivacyToken) error {
	return nil
}

func finalPrivacyFixture(t *testing.T) (*Client, *finalPrivacyStore, func()) {
	t.Helper()
	s := &finalPrivacyStore{entered: make(chan context.Context, 1), release: make(chan struct{})}
	c := NewClient(&store.Device{PrivacyTokens: s}, nil)
	var once sync.Once
	release := func() { once.Do(func() { close(s.release) }) }
	t.Cleanup(func() {
		release()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Errorf("cleanup: %v", err)
		}
	})
	return c, s, release
}
func finalPrivacyEntered(t *testing.T, s *finalPrivacyStore) context.Context {
	t.Helper()
	select {
	case ctx := <-s.entered:
		return ctx
	case <-time.After(2 * time.Second):
		t.Fatal("store not reached")
		return nil
	}
}
func TestFinalPrivacyPruneJoinsActualStore(t *testing.T) {
	c, s, release := finalPrivacyFixture(t)
	c.deleteExpiredPrivacyTokens()
	inherited := finalPrivacyEntered(t, s)
	c.Disconnect()
	if inherited.Err() != nil {
		t.Fatal("ordinary disconnect cancelled prune")
	}
	finalTestBlocked(t, c)
	finalTestWait(t, inherited.Done())
	if !errors.Is(inherited.Err(), context.Canceled) {
		t.Fatal("missing final cancellation")
	}
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("join: %v", err)
	}
}

func TestFinalPrivacyPruneRefusalAndReplacement(t *testing.T) {
	c, s, _ := finalPrivacyFixture(t)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	c.deleteExpiredPrivacyTokens()
	if !c.tcTokenDBPruneLock.TryLock() {
		t.Fatal("refused prune stranded mutex")
	}
	if !c.lastTCTokenDBPrune.IsZero() {
		t.Error("refused launch consumed prune timestamp")
	}
	c.tcTokenDBPruneLock.Unlock()
	select {
	case <-s.entered:
		t.Fatal("store called after seal")
	default:
	}
	replacement, rs, release := finalPrivacyFixture(t)
	replacement.deleteExpiredPrivacyTokens()
	ctx := finalPrivacyEntered(t, rs)
	if ctx.Err() != nil {
		t.Fatal("replacement cancelled by retired client")
	}
	release()
}

type finalPrivacyPrekeys struct{ store.NoopStore }

func (*finalPrivacyPrekeys) UploadedPreKeyCount(context.Context) (int, error) {
	return 0, errors.New("synthetic inventory refusal")
}
func TestFinalPrivacyConnectSuccessPruneChild(t *testing.T) {
	c, s, release := finalPrivacyFixture(t)
	c.Store.PreKeys = &finalPrivacyPrekeys{}
	c.paired.Store(true)
	c.handleConnectSuccess(context.Background(), &waBinary.Node{Tag: "success", Attrs: waBinary.Attrs{"t": "1"}})
	ctx := finalPrivacyEntered(t, s)
	finalTestBlocked(t, c)
	finalTestWait(t, ctx.Done())
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
}

func finalABPropsPending(c *Client) int {
	c.serverABPropsMu.Lock()
	defer c.serverABPropsMu.Unlock()
	return len(c.serverABPropsPending)
}
func TestFinalNotificationABPropsJoinsAndCleans(t *testing.T) {
	for _, final := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller-cancel", true: "final-seal"}[final], func(t *testing.T) {
			c := NewClient(&store.Device{}, nil)
			entered := make(chan context.Context, 1)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(func() {
				unblock()
				if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
					t.Error(err)
				}
			})
			var calls, acks atomic.Int32
			c.ServerABPropsSync = func(ctx context.Context) error { calls.Add(1); entered <- ctx; <-release; return nil }
			parent, cancel := context.WithCancel(context.WithValue(context.Background(), finalConnectionContextKey{}, "notification-value"))
			defer cancel()
			c.deferServerABProps(parent, abpropsNotification(), func() { acks.Add(1) })
			var ctx context.Context
			select {
			case ctx = <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("callback not reached")
			}
			if ctx.Value(finalConnectionContextKey{}) != "notification-value" {
				t.Fatal("caller value lost")
			}
			if d, ok := ctx.Deadline(); !ok || time.Until(d) > 2*time.Minute {
				t.Fatal("2 minute bound lost")
			}
			c.deferServerABProps(parent, abpropsNotification(), func() { acks.Add(1) })
			if calls.Load() != 1 || finalABPropsPending(c) != 1 {
				t.Fatal("dedup lost")
			}
			c.Disconnect()
			if ctx.Err() != nil {
				t.Fatal("ordinary disconnect cancelled task")
			}
			if final {
				finalTestBlocked(t, c)
			} else {
				cancel()
			}
			finalTestWait(t, ctx.Done())
			if !final {
				finalTestBlocked(t, c)
			}
			unblock()
			if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
				t.Fatal(err)
			}
			if acks.Load() != 0 || finalABPropsPending(c) != 0 {
				t.Fatal("cancelled callback acknowledged or pending entry retained")
			}
			c.deferServerABProps(context.Background(), abpropsNotification(), func() { acks.Add(1) })
			if calls.Load() != 1 || acks.Load() != 0 || finalABPropsPending(c) != 0 {
				t.Fatal("refused launch leaked entry or called handler/ACK")
			}
			if finalContextWatcherCount(c) != 0 {
				t.Fatal("task forwarding watcher retained")
			}
		})
	}
}

// Synthetic in-process node relay: no provider, socket, credentials or crypto.
type finalPrivacyRelay struct {
	*fakeShadowRelay
	client  *Client
	entered chan context.Context
	release chan struct{}
}

func (r *finalPrivacyRelay) SendNode(ctx context.Context, data []byte) error {
	unpacked, err := waBinary.Unpack(data)
	if err != nil {
		return err
	}
	node, err := waBinary.Unmarshal(unpacked)
	if err != nil {
		return err
	}
	if node.Tag == "iq" {
		r.client.receiveResponse(ctx, &waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{"id": node.Attrs["id"], "type": "result"}})
		return nil
	}
	if node.Tag == "ib" {
		r.entered <- ctx
		<-r.release
		return ctx.Err()
	}
	return ctx.Err()
}
func TestFinalPrivacyUnifiedBackgroundReuseAndJoin(t *testing.T) {
	c := NewClient(&store.Device{PushName: "synthetic"}, nil)
	entered := make(chan context.Context, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	c.BackgroundEventCtx = context.WithValue(context.Background(), finalConnectionContextKey{}, "background-value")
	c.shadowRelay = &finalPrivacyRelay{fakeShadowRelay: &fakeShadowRelay{}, client: c, entered: entered, release: release}
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.SendPresence(caller, types.PresenceAvailable); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller semantics: %v", err)
	}
	var inherited context.Context
	select {
	case inherited = <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("unified task absent")
	}
	if inherited.Err() != nil || inherited.Value(finalConnectionContextKey{}) != "background-value" {
		t.Fatal("background semantics/value lost")
	}
	c.Disconnect()
	if !c.startUnifiedSession() {
		t.Fatal("ordinary disconnect sealed reuse")
	}
	select {
	case ctx := <-entered:
		if ctx.Err() != nil {
			t.Fatal("reused lifetime cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reused task absent")
	}
	finalTestBlocked(t, c)
	finalTestWait(t, inherited.Done())
	if c.startUnifiedSession() {
		t.Fatal("unified launch after seal")
	}
	unblock()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	if finalContextWatcherCount(c) != 0 {
		t.Fatal("unified forwarding watcher retained")
	}
}

type finalPrivacyPersistence struct{ *finalPrivacyStore }

func (s *finalPrivacyPersistence) GetPrivacyToken(context.Context, types.JID) (*store.PrivacyToken, error) {
	return &store.PrivacyToken{Token: []byte("synthetic")}, nil
}
func (s *finalPrivacyPersistence) PutPrivacyTokens(ctx context.Context, _ ...store.PrivacyToken) error {
	s.entered <- ctx
	<-s.release
	return nil
}
func TestFinalPrivacyPersistenceJoinsActualStore(t *testing.T) {
	c, s, release := finalPrivacyFixture(t)
	c.Store.PrivacyTokens = &finalPrivacyPersistence{s}
	c.BackgroundEventCtx = context.WithValue(context.Background(), finalConnectionContextKey{}, "privacy-value")
	c.shadowRelay = &finalPrivacyRelay{fakeShadowRelay: &fakeShadowRelay{}, client: c}
	if !c.startPrivacyTokenIssue(types.NewJID("synthetic", types.HiddenUserServer), time.Unix(123, 0)) {
		t.Fatal("privacy task refused")
	}
	ctx := finalPrivacyEntered(t, s)
	if ctx.Value(finalConnectionContextKey{}) != "privacy-value" {
		t.Fatal("privacy context value lost")
	}
	finalTestBlocked(t, c)
	finalTestWait(t, ctx.Done())
	if c.startPrivacyTokenIssue(types.EmptyJID, time.Time{}) {
		t.Fatal("privacy launch after seal")
	}
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	if finalContextWatcherCount(c) != 0 {
		t.Fatal("privacy forwarding watcher retained")
	}
}

func TestFinalNotificationABPropsOrdinaryRedelivery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			c := NewClient(&store.Device{}, nil)
			t.Cleanup(func() {
				if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
					t.Error(err)
				}
			})
			contexts := make(chan context.Context, 2)
			var calls, acks atomic.Int32
			c.ServerABPropsSync = func(ctx context.Context) error {
				calls.Add(1)
				contexts <- ctx
				if fail {
					return errors.New("synthetic sync failure")
				}
				return nil
			}
			for i := 0; i < 2; i++ {
				c.deferServerABProps(context.Background(), abpropsNotification(), func() { acks.Add(1) })
				var ctx context.Context
				select {
				case ctx = <-contexts:
				case <-time.After(2 * time.Second):
					t.Fatal("redelivery not admitted")
				}
				// This timeout context is cancelled by the callback's defer after pending cleanup.
				finalTestWait(t, ctx.Done())
				if finalABPropsPending(c) != 0 {
					t.Fatal("completed task retained pending entry")
				}
				c.Disconnect()
			}
			if calls.Load() != 2 || (!fail && acks.Load() != 2) || (fail && acks.Load() != 0) {
				t.Fatal("ordinary redelivery/ACK semantics changed")
			}
		})
	}
}

type finalNotificationLog struct {
	waLog.Logger
	entered, release chan struct{}
	calls            atomic.Int32
}

func (l *finalNotificationLog) Sub(string) waLog.Logger { return l }
func (l *finalNotificationLog) Debugf(msg string, args ...any) {
	if msg == "Parsing privacy settings change notification" {
		l.calls.Add(1)
		close(l.entered)
		<-l.release
	}
}
func TestFinalNotificationHandlerActualJoinAndSealRefusal(t *testing.T) {
	log := &finalNotificationLog{Logger: waLog.Noop, entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	c.SynchronousAck = true
	var once sync.Once
	release := func() { once.Do(func() { close(log.release) }) }
	t.Cleanup(func() {
		release()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	node := &waBinary.Node{Tag: "notification", Attrs: waBinary.Attrs{"type": "account_sync", "from": types.ServerJID, "id": "synthetic"}, Content: []waBinary.Node{{Tag: "privacy"}}}
	c.handleNotification(context.Background(), node)
	finalTestWait(t, log.entered)
	c.Disconnect()
	finalTestBlocked(t, c)
	// The second dispatch must be refused even though the first handler is blocked.
	c.handleNotification(context.Background(), node)
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	if log.calls.Load() != 1 {
		t.Fatal("late notification handler admitted")
	}
}
