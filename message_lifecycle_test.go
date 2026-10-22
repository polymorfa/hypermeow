package whatsmeow

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/proto/waHistorySync"
	waLog "github.com/polymorfa/hypermeow/util/log"
	"google.golang.org/protobuf/proto"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
)

type finalMessageRelay struct {
	*fakeShadowRelay
	entered chan context.Context
	release chan struct{}
}

func (r *finalMessageRelay) SendNode(ctx context.Context, _ []byte) error {
	r.entered <- ctx
	<-r.release
	return ctx.Err()
}
func TestFinalMessageAckJoinsActualSend(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	relay := &finalMessageRelay{fakeShadowRelay: &fakeShadowRelay{}, entered: make(chan context.Context, 1), release: make(chan struct{})}
	c.shadowRelay = relay
	var once sync.Once
	release := func() { once.Do(func() { close(relay.release) }) }
	t.Cleanup(func() {
		release()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	parent := context.WithValue(context.Background(), finalConnectionContextKey{}, "ack-value")
	c.maybeDeferredAck(parent, &waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": "synthetic", "from": types.ServerJID}})
	var ctx context.Context
	select {
	case ctx = <-relay.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("ACK not reached")
	}
	if ctx.Value(finalConnectionContextKey{}) != "ack-value" {
		t.Fatal("ACK value lost")
	}
	c.Disconnect()
	if ctx.Err() != nil {
		t.Fatal("ordinary disconnect cancelled ACK")
	}
	finalTestBlocked(t, c)
	finalTestWait(t, ctx.Done())
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
}

type finalMessageStoreCall struct {
	ctx  context.Context
	user types.JID
	name string
}
type finalMessageStore struct {
	store.NoopStore
	entered chan finalMessageStoreCall
	release chan struct{}
}

func (s *finalMessageStore) PutPushName(ctx context.Context, user types.JID, name string) (bool, string, error) {
	s.entered <- finalMessageStoreCall{ctx, user, name}
	<-s.release
	return false, "", nil
}
func (s *finalMessageStore) DeleteOldBufferedHashes(ctx context.Context) error {
	s.entered <- finalMessageStoreCall{ctx: ctx}
	<-s.release
	return nil
}
func finalMessageFixture(t *testing.T) (*Client, *finalMessageStore, func()) {
	t.Helper()
	s := &finalMessageStore{entered: make(chan finalMessageStoreCall, 2), release: make(chan struct{})}
	c := NewClient(&store.Device{Contacts: s, EventBuffer: s}, nil)
	var once sync.Once
	release := func() { once.Do(func() { close(s.release) }) }
	t.Cleanup(func() {
		release()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	return c, s, release
}
func finalMessageStoreEntered(t *testing.T, s *finalMessageStore) finalMessageStoreCall {
	t.Helper()
	select {
	case got := <-s.entered:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("store not reached")
		return finalMessageStoreCall{}
	}
}

type finalMessageParent struct {
	context.Context
	entered, release chan struct{}
	once             sync.Once
}

func (p *finalMessageParent) Value(key any) any {
	p.once.Do(func() { close(p.entered); <-p.release })
	return p.Context.Value(key)
}
func TestFinalMessageNameArgumentsAndActualStore(t *testing.T) {
	c, s, release := finalMessageFixture(t)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	parent := &finalMessageParent{Context: base, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	unblock := func() { once.Do(func() { close(parent.release) }) }
	// This cleanup runs before fixture teardown, including assertion failures.
	launched := make(chan struct{})
	t.Cleanup(func() { unblock(); finalTestWait(t, launched) })
	info := &types.MessageInfo{MessageSource: types.MessageSource{Sender: types.NewJID("old", types.DefaultUserServer)}, PushName: "old-name"}
	go func() { c.updateMessageContactNames(parent, info); close(launched) }()
	finalTestWait(t, parent.entered) // Context construction precedes goroutine scheduling.
	info.Sender = types.NewJID("new", types.DefaultUserServer)
	info.PushName = "new-name"
	unblock()
	finalTestWait(t, launched)
	got := finalMessageStoreEntered(t, s)
	if got.user.User != "old" || got.name != "old-name" {
		t.Fatalf("go-call values moved into worker: %v %q", got.user, got.name)
	}
	finalTestBlocked(t, c)
	finalTestWait(t, got.ctx.Done())
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
}
func TestFinalMessageBufferWithoutCancelAndRefusal(t *testing.T) {
	c, s, release := finalMessageFixture(t)
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), finalConnectionContextKey{}, "buffer-value"))
	defer cancel()
	if !c.startEventBufferCleanup(parent) {
		t.Fatal("cleanup refused")
	}
	got := finalMessageStoreEntered(t, s)
	cancel()
	c.Disconnect()
	if got.ctx.Err() != nil || got.ctx.Value(finalConnectionContextKey{}) != "buffer-value" {
		t.Fatal("WithoutCancel semantics lost")
	}
	if c.startEventBufferCleanup(context.Background()) {
		t.Fatal("cleanup not coalesced")
	}
	finalTestBlocked(t, c)
	finalTestWait(t, got.ctx.Done())
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	c.decryptedBufferClearMu.Lock()
	busy := c.decryptedBufferClearStarted
	c.decryptedBufferClearMu.Unlock()
	if busy {
		t.Fatal("cleanup guard retained")
	}
	retired, _, _ := finalMessageFixture(t)
	if err := retired.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	if retired.startEventBufferCleanup(context.Background()) {
		t.Fatal("post-seal cleanup admitted")
	}
	retired.decryptedBufferClearMu.Lock()
	defer retired.decryptedBufferClearMu.Unlock()
	if retired.decryptedBufferClearStarted || !retired.lastDecryptedBufferClear.IsZero() {
		t.Fatal("refusal stranded guard or advanced timestamp")
	}
}

func finalMessageHistoryPayload(t *testing.T) *waE2E.HistorySyncNotification {
	t.Helper()
	raw, err := proto.Marshal(&waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_PUSH_NAME.Enum(), Pushnames: []*waHistorySync.Pushname{{ID: proto.String("synthetic@s.whatsapp.net"), Pushname: proto.String("synthetic")}}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err = w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return &waE2E.HistorySyncNotification{InitialHistBootstrapInlinePayload: buf.Bytes()}
}
func TestFinalMessageHistoryStorageContexts(t *testing.T) {
	for _, synchronous := range []bool{false, true} {
		t.Run(map[bool]string{false: "async", true: "sync"}[synchronous], func(t *testing.T) {
			c, s, release := finalMessageFixture(t)
			parent, cancel := context.WithCancel(context.WithValue(context.Background(), finalConnectionContextKey{}, "history-value"))
			defer cancel()
			returned := make(chan error, 1)
			exited := make(chan struct{})
			t.Cleanup(func() { release(); finalTestWait(t, exited) })
			notif := finalMessageHistoryPayload(t)
			go func() {
				defer close(exited)
				_, err := c.DownloadHistorySync(parent, notif, synchronous)
				returned <- err
			}()
			got := finalMessageStoreEntered(t, s)
			if got.ctx.Value(finalConnectionContextKey{}) != "history-value" {
				t.Fatal("history value lost")
			}
			cancel()
			c.Disconnect()
			if synchronous {
				if got.ctx != parent || !errors.Is(got.ctx.Err(), context.Canceled) {
					t.Fatal("synchronous storage context changed")
				}
				select {
				case <-returned:
					t.Fatal("synchronous storage returned early")
				default:
				}
				release()
			} else {
				if got.ctx.Err() != nil {
					t.Fatal("async storage lost WithoutCancel")
				}
				select {
				case err := <-returned:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("async download did not return")
				}
				finalTestBlocked(t, c)
				finalTestWait(t, got.ctx.Done())
				release()
			}
			if synchronous {
				select {
				case err := <-returned:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("sync download stuck")
				}
			}
			if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
				t.Fatal(err)
			}
		})
	}
}

type finalMessageHistoryLog struct {
	waLog.Logger
	first, second, releaseFirst, releaseSecond chan struct{}
	calls                                      atomic.Int32
	restarted                                  chan struct{}
}

func (l *finalMessageHistoryLog) Sub(string) waLog.Logger { return l }
func (l *finalMessageHistoryLog) Errorf(msg string, args ...any) {
	if msg == "Failed to download history sync: %v" {
		if l.calls.Add(1) == 1 {
			close(l.first)
			<-l.releaseFirst
			panic("synthetic loop restart")
		} else {
			close(l.second)
			<-l.releaseSecond
		}
	}
}
func (l *finalMessageHistoryLog) Warnf(msg string, args ...any) {
	if msg == "New history sync notifications appeared after loop stopped, restarting loop..." {
		close(l.restarted)
	}
}
func TestFinalMessageHistoryRestartAndExactClaim(t *testing.T) {
	log := &finalMessageHistoryLog{Logger: waLog.Noop, first: make(chan struct{}), second: make(chan struct{}), releaseFirst: make(chan struct{}), releaseSecond: make(chan struct{}), restarted: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	var once1, once2 sync.Once
	release1 := func() { once1.Do(func() { close(log.releaseFirst) }) }
	release2 := func() { once2.Do(func() { close(log.releaseSecond) }) }
	t.Cleanup(func() {
		release1()
		release2()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	bad := historySyncNotification{notification: &waE2E.HistorySyncNotification{InitialHistBootstrapInlinePayload: []byte("not zlib")}}
	if !c.enqueueHistorySyncNotification(bad) {
		t.Fatal("queue refused")
	}
	finalTestWait(t, log.first)
	c.historySyncHandlerMu.Lock()
	old := c.historySyncHandlerOwner
	c.historySyncHandlerMu.Unlock()
	if !c.enqueueHistorySyncNotification(bad) {
		t.Fatal("queue refused while loop running")
	}
	c.Disconnect()
	release1()
	finalTestWait(t, log.restarted)
	finalTestWait(t, log.second)
	if c.releaseHistorySyncLoop(old) {
		t.Fatal("old owner cleared replacement")
	}
	if !c.historySyncHandlerStarted.Load() {
		t.Fatal("replacement claim cleared")
	}
	finalTestBlocked(t, c)
	release2()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	if c.historySyncHandlerStarted.Load() {
		t.Fatal("loop claim stranded")
	}
	if c.startHistorySyncLoop(false) || c.historySyncHandlerStarted.Load() {
		t.Fatal("refused loop retained claim")
	}
	if c.enqueueHistorySyncNotification(bad) {
		t.Fatal("post-seal queue accepted")
	}
}
func TestFinalMessageHistoryBlockedEnqueueCancels(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	// An unbuffered queue with no consumer exercises the original backpressure.
	c.historySyncNotifications = make(chan historySyncNotification)
	done, ok := c.enterNative()
	if !ok {
		t.Fatal("fixture admission")
	}
	result := make(chan bool, 1)
	ready := make(chan struct{})
	go func() {
		defer done()
		close(ready)
		result <- c.enqueueHistorySyncNotification(historySyncNotification{})
	}()
	t.Cleanup(func() {
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	finalTestWait(t, ready)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	select {
	case accepted := <-result:
		if accepted {
			t.Fatal("blocked enqueue accepted after seal")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue stranded")
	}
	if c.historySyncHandlerStarted.Load() {
		t.Fatal("enqueue created late loop")
	}
}

type finalMessagePhoneLog struct {
	waLog.Logger
	entered, release chan struct{}
}

func (l *finalMessagePhoneLog) Sub(string) waLog.Logger { return l }
func (l *finalMessagePhoneLog) Debugf(msg string, args ...any) {
	if msg == "Cancelled delayed request for message %s from phone" {
		close(l.entered)
		<-l.release
	}
}
func TestFinalMessageDelayedPhoneCancelJoinsAndProtectsReplacement(t *testing.T) {
	log := &finalMessagePhoneLog{Logger: waLog.Noop, entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	c.AutomaticMessageRerequestFromPhone = true
	// Permanent cancellation reaches the real cancellation path without changing
	// the original five-second policy timer or invoking crypto.
	c.BackgroundEventCtx = context.Background()
	var once sync.Once
	release := func() { once.Do(func() { close(log.release) }) }
	t.Cleanup(func() {
		release()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	if !c.startDelayedRequestMessageFromPhone(&types.MessageInfo{ID: "synthetic"}) {
		t.Fatal("delayed task refused")
	}
	c.Disconnect()
	finalTestBlocked(t, c)
	finalTestWait(t, log.entered)
	c.pendingPhoneRerequestsLock.Lock()
	old := c.pendingPhoneRerequests["synthetic"]
	replacement := &pendingPhoneRerequest{cancel: func() {}}
	c.pendingPhoneRerequests["synthetic"] = replacement
	c.pendingPhoneRerequestsLock.Unlock()
	if old == nil {
		t.Fatal("cancel map entry absent")
	}
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	c.pendingPhoneRerequestsLock.Lock()
	got := c.pendingPhoneRerequests["synthetic"]
	delete(c.pendingPhoneRerequests, "synthetic")
	c.pendingPhoneRerequestsLock.Unlock()
	if got != replacement {
		t.Fatal("old cleanup cleared replacement")
	}
	if c.startDelayedRequestMessageFromPhone(&types.MessageInfo{ID: "late"}) {
		t.Fatal("late delayed task admitted")
	}
	c.pendingPhoneRerequestsLock.RLock()
	n := len(c.pendingPhoneRerequests)
	c.pendingPhoneRerequestsLock.RUnlock()
	if n != 0 {
		t.Fatal("refused launch left cancel entry")
	}
}
func TestFinalMessageSynchronousAckBranches(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	t.Cleanup(func() {
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	c.SynchronousAck = true
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	c.backgroundIfAsyncAckContext(parent, func(ctx context.Context) {
		if ctx != parent {
			t.Error("synchronous context changed")
		}
		called = true
	})
	if !called {
		t.Fatal("synchronous callback delayed")
	}
	legacy := false
	c.backgroundIfAsyncAck(func() { legacy = true })
	if !legacy {
		t.Fatal("synchronous fn delayed")
	}
	relay := &finalMessageRelay{fakeShadowRelay: &fakeShadowRelay{}, entered: make(chan context.Context, 4), release: make(chan struct{})}
	close(relay.release) // A regression must fail assertions rather than strand the fixture.
	c.shadowRelay = relay
	// Both synchronous cancellation paths must suppress ACK without reaching send.
	node := &waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": "synthetic", "from": types.ServerJID}}
	c.maybeDeferredAck(parent, node)()
	cancelled := true
	c.maybeDeferredAck(context.Background(), node)(&cancelled)
	select {
	case <-relay.entered:
		t.Fatal("cancelled synchronous ACK sent")
	default:
	}
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	c.SynchronousAck = false
	called = false
	c.backgroundIfAsyncAck(func() { called = true })
	c.backgroundIfAsyncAckContext(context.Background(), func(context.Context) { called = true })
	if called {
		t.Fatal("async fn admitted after seal")
	}
}

func TestFinalMessageDelayedPhoneOrdinaryCancelCleanup(t *testing.T) {
	log := &finalMessagePhoneLog{Logger: waLog.Noop, entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	c.AutomaticMessageRerequestFromPhone = true
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	c.BackgroundEventCtx = parent
	var once sync.Once
	release := func() { once.Do(func() { close(log.release) }) }
	t.Cleanup(func() {
		release()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	if !c.startDelayedRequestMessageFromPhone(&types.MessageInfo{ID: "ordinary"}) {
		t.Fatal("task refused")
	}
	finalTestWait(t, log.entered)
	// Real pending-map cancel and clear operate on the exact registered owner.
	c.cancelDelayedRequestFromPhone("ordinary")
	c.clearDelayedMessageRequests()
	// Call the same private body synchronously while the first owner is held,
	// so this assertion cannot become a later fresh request during teardown.
	c.delayedRequestMessageFromPhone(&types.MessageInfo{ID: "ordinary"})
	finalTestBlocked(t, c)
	release()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	c.pendingPhoneRerequestsLock.RLock()
	n := len(c.pendingPhoneRerequests)
	c.pendingPhoneRerequestsLock.RUnlock()
	if n != 0 {
		t.Fatal("ordinary cancellation retained pending owner")
	}
	if finalContextWatcherCount(c) != 0 {
		t.Fatal("task forwarding watcher retained")
	}
}
func TestFinalMessageAsyncFunctionActualJoin(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	c.backgroundIfAsyncAck(func() { close(entered); <-release })
	finalTestWait(t, entered)
	finalTestBlocked(t, c)
	unblock()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
}
