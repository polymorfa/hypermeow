package whatsmeow

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waCompanionReg"
	"github.com/polymorfa/hypermeow/proto/waWa6"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	waLog "github.com/polymorfa/hypermeow/util/log"
	"google.golang.org/protobuf/proto"
)

type finalPairLog struct {
	waLog.Logger
	entered, release chan struct{}
}

func (l *finalPairLog) Sub(string) waLog.Logger { return l }
func (l *finalPairLog) Errorf(msg string, args ...any) {
	if msg == "Failed to pair device: %v" {
		close(l.entered)
		<-l.release
	}
}
func finalPairFailureNode() *waBinary.Node {
	return &waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{"id": "original-id", "t": "1"}, Content: []waBinary.Node{{Tag: "pair-success", Content: []waBinary.Node{
		{Tag: "device-identity", Content: []byte{0xff}}, // Rejected protobuf, before any key/crypto operation.
		{Tag: "biz", Attrs: waBinary.Attrs{"name": "original-biz"}},
		{Tag: "platform", Attrs: waBinary.Attrs{"name": "original-platform"}},
		{Tag: "device", Attrs: waBinary.Attrs{"jid": types.NewJID("synthetic", types.DefaultUserServer)}},
	}}}}
}
func TestFinalPairOuterActualErrorContinuationJoin(t *testing.T) {
	log := &finalPairLog{Logger: waLog.Noop, entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	relay := &finalMessageRelay{fakeShadowRelay: &fakeShadowRelay{}, entered: make(chan context.Context, 1), release: make(chan struct{})}
	c.shadowRelay = relay
	var onceSend, onceLog sync.Once
	releaseSend := func() { onceSend.Do(func() { close(relay.release) }) }
	releaseLog := func() { onceLog.Do(func() { close(log.release) }) }
	t.Cleanup(func() {
		releaseSend()
		releaseLog()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	parent := context.WithValue(context.Background(), finalConnectionContextKey{}, "pair-value")
	c.handlePairSuccess(parent, finalPairFailureNode())
	var ctx context.Context
	select {
	case ctx = <-relay.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("actual pair error send not reached")
	}
	c.Disconnect()
	if ctx.Err() != nil || ctx.Value(finalConnectionContextKey{}) != "pair-value" {
		t.Fatal("outer ancestry/value changed")
	}
	finalTestBlocked(t, c)
	finalTestWait(t, ctx.Done())
	releaseSend()
	finalTestWait(t, log.entered)
	finalTestBlocked(t, c) // handlePair returned; its existing error continuation has not.
	releaseLog()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
}

func TestFinalPairCallbackNormalValuesNilFalseAndPayload(t *testing.T) {
	jid := types.NewJID("123", types.DefaultUserServer)
	c := NewClient(&store.Device{ID: &jid}, nil)
	t.Cleanup(func() {
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	allowed, err := c.prePairCallbackDecision(jid, "platform", "business")
	if !allowed || err != nil {
		t.Fatalf("nil callback: %v %v", allowed, err)
	}
	var calls int
	c.PrePairCallback = func(got types.JID, platform, business string) bool {
		calls++
		if got != jid || platform != "platform" || business != "business" {
			t.Error("callback inputs changed")
		}
		return false
	}
	c.Disconnect()
	allowed, err = c.prePairCallbackDecision(jid, "platform", "business")
	if allowed || err != nil || calls != 1 {
		t.Fatalf("normal false: %v %v calls%d", allowed, err, calls)
	}
	payload := &waWa6.ClientPayload{Username: proto.Uint64(321), Passive: proto.Bool(true)}
	c.GetClientPayload = func() *waWa6.ClientPayload { return payload }
	got, err := c.clientPayloadForHandshake()
	if got != payload || err != nil {
		t.Fatalf("custom payload changed: %p %v", got, err)
	}
	wantBytes, err := proto.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := proto.Marshal(got)
	if err != nil || !bytes.Equal(wantBytes, gotBytes) {
		t.Fatal("payload bytes changed")
	}
	c.GetClientPayload = nil
	got, err = c.clientPayloadForHandshake()
	if err != nil || !proto.Equal(got, c.Store.GetClientPayload()) {
		t.Fatalf("built-in fallback changed: %v", err)
	}
	// A normal nil custom result remains nil; it is not replaced with fallback.
	c.Store.ID = &types.EmptyJID
	c.GetClientPayload = func() *waWa6.ClientPayload { return nil }
	got, err = c.clientPayloadForHandshake()
	if got != nil || err != nil {
		t.Fatal("normal nil payload behavior changed")
	}
	if err = c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	c.PrePairCallback = func(types.JID, string, string) bool { t.Error("sealed PrePair called"); return true }
	c.GetClientPayload = func() *waWa6.ClientPayload { t.Error("sealed payload called"); return payload }
	if allowed, err = c.prePairCallbackDecision(jid, "platform", "business"); allowed || !errors.Is(err, ErrClientClosed) {
		t.Fatalf("sealed PrePair: %v %v", allowed, err)
	}
	if got, err = c.clientPayloadForHandshake(); got != nil || !errors.Is(err, ErrClientClosed) {
		t.Fatalf("sealed payload: %v %v", got, err)
	}
	c.GetClientPayload = nil // Empty ID would panic if refusal ran built-in fallback.
	if got, err = c.clientPayloadForHandshake(); got != nil || !errors.Is(err, ErrClientClosed) {
		t.Fatal("sealed request fell back")
	}
}

func TestFinalPairCallbackActualReturnReentryAndIsolation(t *testing.T) {
	for _, kind := range []string{"prepair", "payload"} {
		t.Run(kind, func(t *testing.T) {
			c := NewClient(&store.Device{}, nil)
			replacement := NewClient(&store.Device{}, nil)
			entered, release, reentering := make(chan struct{}), make(chan struct{}), make(chan struct{})
			selfResult := make(chan error, 1)
			result := make(chan error, 1)
			waiter, cancel := context.WithCancel(finalTestContext(t))
			defer cancel()
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(func() {
				cancel()
				unblock()
				if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
					t.Error(err)
				}
				if err := replacement.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
					t.Error(err)
				}
			})
			hook := func() { close(entered); <-release; close(reentering); selfResult <- c.FinalClose(waiter) }
			payload := &waWa6.ClientPayload{Username: proto.Uint64(99)}
			if kind == "prepair" {
				c.PrePairCallback = func(types.JID, string, string) bool { hook(); return true }
				go func() {
					allowed, err := c.prePairCallbackDecision(types.EmptyJID, "p", "b")
					if allowed && err == nil {
						result <- errors.New("late true decision accepted")
					} else {
						result <- err
					}
				}()
			} else {
				c.GetClientPayload = func() *waWa6.ClientPayload { hook(); return payload }
				go func() {
					// Real Connect's existing caller holds this lock during doHandshake.
					c.socketLock.Lock()
					defer c.socketLock.Unlock()
					got, err := c.clientPayloadForHandshake()
					if got != nil {
						result <- errors.New("late payload returned")
					} else {
						result <- err
					}
				}()
			}
			finalTestWait(t, entered)
			if kind == "prepair" {
				c.Disconnect()
			}
			// In the payload case ordinary Disconnect would wait on the existing caller's
			// socket lock. It is tested separately outside that lock in the normal case.
			finalTestBlocked(t, c)
			replacement.PrePairCallback = func(types.JID, string, string) bool { return true }
			replacement.GetClientPayload = func() *waWa6.ClientPayload { return payload }
			if allowed, err := replacement.prePairCallbackDecision(types.EmptyJID, "other", "owner"); !allowed || err != nil {
				t.Fatal("replacement PrePair affected")
			}
			if got, err := replacement.clientPayloadForHandshake(); got != payload || err != nil {
				t.Fatal("replacement payload affected")
			}
			unblock()
			finalTestWait(t, reentering)
			cancel()
			select {
			case err := <-selfResult:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("self wait: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("callback reentry stuck")
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrClientClosed) {
					t.Fatalf("late result: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("actual callback return not joined")
			}
			if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
				t.Fatal(err)
			}
		})
	}
}

func TestFinalPairCallbackPanicsReleaseAdmission(t *testing.T) {
	for _, kind := range []string{"prepair", "payload", "fallback"} {
		t.Run(kind, func(t *testing.T) {
			c := NewClient(&store.Device{}, nil)
			t.Cleanup(func() {
				if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
					t.Error(err)
				}
			})
			c.PrePairCallback = func(types.JID, string, string) bool { panic("synthetic PrePair panic") }
			c.GetClientPayload = func() *waWa6.ClientPayload { panic("synthetic payload panic") }
			if kind == "fallback" {
				c.GetClientPayload = nil
				c.Store.ID = &types.EmptyJID
			}
			recovered := false
			func() {
				defer func() { recovered = recover() != nil }()
				if kind == "prepair" {
					_, _ = c.prePairCallbackDecision(types.EmptyJID, "p", "b")
				} else {
					_, _ = c.clientPayloadForHandshake()
				}
			}()
			if !recovered {
				t.Fatal("panic policy changed")
			}
			c.lifetime.mu.Lock()
			active := c.lifetime.active
			c.lifetime.mu.Unlock()
			if active != 0 {
				t.Fatal("panic stranded callback count")
			}
		})
	}
}

type finalPairCaptureRelay struct {
	*fakeShadowRelay
	sent     chan *waBinary.Node
	contexts chan context.Context
}

func (r *finalPairCaptureRelay) SendNode(ctx context.Context, data []byte) error {
	raw, err := waBinary.Unpack(data)
	if err != nil {
		return err
	}
	node, err := waBinary.Unmarshal(raw)
	if err != nil {
		return err
	}
	r.sent <- node
	r.contexts <- ctx
	return nil
}
func TestFinalPairOuterPrelaunchCaptureAndCancelledContext(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	base, cancel := context.WithCancel(context.WithValue(context.Background(), finalConnectionContextKey{}, "captured-value"))
	defer cancel()
	parent := &finalMessageParent{Context: base, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	unblock := func() { once.Do(func() { close(parent.release) }) }
	launched := make(chan struct{})
	t.Cleanup(func() {
		unblock()
		finalTestWait(t, launched)
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	relay := &finalPairCaptureRelay{fakeShadowRelay: &fakeShadowRelay{}, sent: make(chan *waBinary.Node, 1), contexts: make(chan context.Context, 1)}
	c.shadowRelay = relay
	event := make(chan *events.PairError, 1)
	c.AddEventHandler(func(evt any) {
		if v, ok := evt.(*events.PairError); ok {
			event <- v
		}
	})
	node := finalPairFailureNode()
	propsBytes, err := proto.Marshal(&waCompanionReg.ClientPairingProps{IsChatDbLidMigrated: proto.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	pairSuccess := node.GetChildByTag("pair-success")
	children := pairSuccess.GetChildren()
	children = append(children, waBinary.Node{Tag: "client-props", Content: propsBytes})
	node.Content = []waBinary.Node{{Tag: "pair-success", Content: children}}
	go func() { c.handlePairSuccess(parent, node); close(launched) }()
	finalTestWait(t, parent.entered) // Parsed locals/props exist; task is not scheduled.
	node.Attrs["id"] = "replacement-id"
	node.GetChildByTag("pair-success", "biz").Attrs["name"] = "replacement-biz"
	node.GetChildByTag("pair-success", "platform").Attrs["name"] = "replacement-platform"
	cancel()
	c.Disconnect()
	unblock()
	finalTestWait(t, launched)
	select {
	case sent := <-relay.sent:
		if sent.Attrs["id"] != "original-id" {
			t.Fatal("reqID read moved after launch")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("actual error send absent")
	}
	select {
	case ctx := <-relay.contexts:
		if !errors.Is(ctx.Err(), context.Canceled) || ctx.Value(finalConnectionContextKey{}) != "captured-value" {
			t.Fatal("caller cancellation/values changed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("send context absent")
	}
	select {
	case evt := <-event:
		if evt.BusinessName != "original-biz" || evt.Platform != "original-platform" || !evt.Props.GetIsChatDbLidMigrated() {
			t.Fatal("original local/props capture changed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PairError continuation absent")
	}
}
func TestFinalPairOuterPostSealRefusesAsyncBody(t *testing.T) {
	log := &finalPairLog{Logger: waLog.Noop, entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	relay := &finalMessageRelay{fakeShadowRelay: &fakeShadowRelay{}, entered: make(chan context.Context, 1), release: make(chan struct{})}
	c.shadowRelay = relay
	var once sync.Once
	unblock := func() { once.Do(func() { close(relay.release); close(log.release) }) }
	t.Cleanup(func() {
		unblock()
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Error(err)
		}
	})
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatal(err)
	}
	c.handlePairSuccess(context.Background(), finalPairFailureNode())
	select {
	case <-relay.entered:
		t.Fatal("refused outer task sent error")
	default:
	}
	select {
	case <-log.entered:
		t.Fatal("refused outer task ran error continuation")
	default:
	}
	if finalContextWatcherCount(c) != 0 {
		t.Fatal("refused outer task retained context")
	}
}
