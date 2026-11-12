package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

func guardedTestClient(sends *atomic.Int32, sendErr error) *Client {
	return &Client{testSendHook: func([]byte) error { sends.Add(1); return sendErr }}
}

var guardedTestNode = waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": "guarded-1"}}

func TestGuardedSendNilHooksKeepsExistingPath(t *testing.T) {
	var sends atomic.Int32
	data, err := guardedTestClient(&sends, nil).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", nil)
	if err != nil || len(data) == 0 || sends.Load() != 1 {
		t.Fatalf("nil hooks must use the existing sender: err=%v sends=%d", err, sends.Load())
	}
}

func TestGuardedSendRefusalWritesNothing(t *testing.T) {
	var sends atomic.Int32
	entered := false
	hooks := &GuardedSendHooks{
		Admit: func(context.Context, types.MessageID, func(context.Context) error) error {
			return errors.New("authority lost")
		},
		Entered: func() { entered = true },
	}
	_, err := guardedTestClient(&sends, nil).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
	if !errors.Is(err, ErrGuardedSendRefused) || errors.Is(err, ErrGuardedSendUncertain) || entered || sends.Load() != 0 {
		t.Fatalf("refusal must write nothing: err=%v entered=%v sends=%d", err, entered, sends.Load())
	}
}

func TestGuardedSendAdmittedWritesOnceAfterEntered(t *testing.T) {
	var sends atomic.Int32
	var order []string
	cli := &Client{testSendHook: func([]byte) error { order = append(order, "send"); sends.Add(1); return nil }}
	hooks := &GuardedSendHooks{
		Admit: func(ctx context.Context, id types.MessageID, write func(context.Context) error) error {
			if id != "guarded-1" {
				t.Errorf("unexpected id %q", id)
			}
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > GuardAdmitTimeout {
				t.Error("Admit must run under the bounded admission deadline")
			}
			if err := write(context.Background()); err != nil {
				return err
			}
			if !errors.Is(write(context.Background()), ErrGuardedSendRefused) {
				t.Error("a second write must be refused")
			}
			return nil
		},
		Entered: func() { order = append(order, "entered") },
	}
	data, err := cli.sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
	if err != nil || len(data) == 0 || sends.Load() != 1 || len(order) != 2 || order[0] != "entered" || order[1] != "send" {
		t.Fatalf("admitted send: err=%v sends=%d order=%v", err, sends.Load(), order)
	}
}

func TestGuardedSendRetainedWriteIsClosed(t *testing.T) {
	var sends atomic.Int32
	var retained func(context.Context) error
	hooks := &GuardedSendHooks{
		Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
			retained = write
			return nil
		},
		Entered: func() {},
	}
	_, err := guardedTestClient(&sends, nil).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
	if !errors.Is(err, ErrGuardedSendRefused) {
		t.Fatalf("returning without writing must refuse: %v", err)
	}
	if !errors.Is(retained(context.Background()), ErrGuardedSendRefused) || sends.Load() != 0 {
		t.Fatalf("a retained write after Admit returned must not send: sends=%d", sends.Load())
	}
}

// A write racing Admit's return either refuses or completes before the wrapper returns.
// No write may start or still be running after sendGuardedMessageNode returns.
func TestGuardedSendConcurrentReturnHasNoGap(t *testing.T) {
	for i := 0; i < 500; i++ {
		var sends atomic.Int32
		cli := guardedTestClient(&sends, nil)
		var wg sync.WaitGroup
		hooks := &GuardedSendHooks{
			Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
				wg.Add(1)
				go func() { defer wg.Done(); _ = write(context.Background()) }()
				return nil
			},
			Entered: func() {},
		}
		_, err := cli.sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
		afterReturn := sends.Load()
		wg.Wait()
		if sends.Load() != afterReturn {
			t.Fatalf("iteration %d: a write ran after the wrapper returned", i)
		}
		if (afterReturn == 0) != errors.Is(err, ErrGuardedSendRefused) || afterReturn > 1 {
			t.Fatalf("iteration %d: sends=%d err=%v", i, afterReturn, err)
		}
	}
}

func TestGuardedSendFailureAfterHandoffIsUncertain(t *testing.T) {
	var sends atomic.Int32
	hooks := &GuardedSendHooks{
		Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
			return write(context.Background())
		},
		Entered: func() {},
	}
	_, err := guardedTestClient(&sends, errors.New("socket lost")).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
	if !errors.Is(err, ErrGuardedSendUncertain) || errors.Is(err, ErrGuardedSendRefused) || sends.Load() != 1 {
		t.Fatalf("a failure after handoff must be uncertain, never refused: err=%v", err)
	}
}

func TestGuardedSendRequiresHooksAndID(t *testing.T) {
	var sends atomic.Int32
	cli := guardedTestClient(&sends, nil)
	for _, hooks := range []*GuardedSendHooks{{Entered: func() {}}, {Admit: func(context.Context, types.MessageID, func(context.Context) error) error { return nil }}} {
		if _, err := cli.sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks); !errors.Is(err, ErrGuardedSendUnsupported) {
			t.Fatalf("incomplete hooks must be unsupported: %v", err)
		}
	}
	full := &GuardedSendHooks{Admit: func(context.Context, types.MessageID, func(context.Context) error) error { return nil }, Entered: func() {}}
	if _, err := cli.sendGuardedMessageNode(t.Context(), guardedTestNode, "", full); !errors.Is(err, ErrGuardedSendUnsupported) {
		t.Fatalf("an empty ID must be unsupported: %v", err)
	}
	if _, err := (&Client{}).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", full); !errors.Is(err, ErrNotConnected) || sends.Load() != 0 {
		t.Fatalf("no socket must refuse before Admit: %v", err)
	}
}

func TestSendMessageRefusesUnsupportedGuardedSends(t *testing.T) {
	hooks := &GuardedSendHooks{Admit: func(context.Context, types.MessageID, func(context.Context) error) error { return nil }, Entered: func() {}}
	text := &waE2E.Message{Conversation: proto.String("hi")}
	user := types.NewJID("15550100001", types.DefaultUserServer)
	group := types.NewJID("120363000000000000", types.GroupServer)
	for name, tc := range map[string]struct {
		to    types.JID
		msg   *waE2E.Message
		extra SendRequestExtra
	}{
		"no id":            {user, text, SendRequestExtra{GuardedSend: hooks}},
		"group":            {group, text, SendRequestExtra{ID: "guarded-1", GuardedSend: hooks}},
		"peer":             {user, text, SendRequestExtra{ID: "guarded-1", GuardedSend: hooks, Peer: true}},
		"media":            {user, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, SendRequestExtra{ID: "guarded-1", GuardedSend: hooks}},
		"extra":            {user, &waE2E.Message{Conversation: proto.String("hi"), ImageMessage: &waE2E.ImageMessage{}}, SendRequestExtra{ID: "guarded-1", GuardedSend: hooks}},
		"meta node":        {user, text, SendRequestExtra{ID: "guarded-1", GuardedSend: hooks, Meta: &types.MsgMetaInfo{}}},
		"negative timeout": {user, text, SendRequestExtra{ID: "guarded-1", GuardedSend: hooks, Timeout: -1}},
	} {
		if _, err := (&Client{}).SendMessage(t.Context(), tc.to, tc.msg, tc.extra); !errors.Is(err, ErrGuardedSendUnsupported) {
			t.Errorf("%s: want ErrGuardedSendUnsupported, got %v", name, err)
		}
	}
}

func TestGuardedSendDeadlineIsEnforcedWhenAdmitIgnoresIt(t *testing.T) {
	previous := guardAdmitTimeout
	guardAdmitTimeout = 50 * time.Millisecond
	t.Cleanup(func() { guardAdmitTimeout = previous })
	var sends atomic.Int32
	release := make(chan struct{})
	lateWrite := make(chan error, 1)
	hooks := &GuardedSendHooks{
		Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
			<-release // ignores its context
			lateWrite <- write(context.Background())
			return nil
		},
		Entered: func() {},
	}
	start := time.Now()
	_, err := guardedTestClient(&sends, nil).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
	if !errors.Is(err, ErrGuardedSendRefused) || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("a stuck Admit must be refused at the deadline: err=%v after %v", err, time.Since(start))
	}
	close(release)
	if !errors.Is(<-lateWrite, ErrGuardedSendRefused) || sends.Load() != 0 {
		t.Fatalf("a write after the deadline must not send: sends=%d", sends.Load())
	}
}

func TestGuardedSendEnteredPanicIsUncertain(t *testing.T) {
	var sends atomic.Int32
	hooks := &GuardedSendHooks{
		Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
			_ = write(context.Background())
			return nil
		},
		Entered: func() { panic("entered failed") },
	}
	_, err := guardedTestClient(&sends, nil).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
	if !errors.Is(err, ErrGuardedSendUncertain) {
		t.Fatalf("a panic after write began must be uncertain, never success: %v", err)
	}
}

func TestGuardedSendAdmitPanicIsRefused(t *testing.T) {
	var sends atomic.Int32
	hooks := &GuardedSendHooks{
		Admit:   func(context.Context, types.MessageID, func(context.Context) error) error { panic("admit failed") },
		Entered: func() {},
	}
	_, err := guardedTestClient(&sends, nil).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
	if !errors.Is(err, ErrGuardedSendRefused) || sends.Load() != 0 {
		t.Fatalf("a panic before write must refuse: %v", err)
	}
}

func TestGuardedSendRefusalAfterHandoffIsOnlyUncertain(t *testing.T) {
	var sends atomic.Int32
	hooks := &GuardedSendHooks{
		Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
			if err := write(context.Background()); err != nil {
				return err
			}
			return fmt.Errorf("late check: %w", ErrGuardedSendRefused)
		},
		Entered: func() {},
	}
	_, err := guardedTestClient(&sends, nil).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
	if !errors.Is(err, ErrGuardedSendUncertain) || errors.Is(err, ErrGuardedSendRefused) || sends.Load() != 1 {
		t.Fatalf("an admission error after handoff must be uncertain only: %v", err)
	}
}

func TestSendMessageGuardedDoesNotMutateCallerMessage(t *testing.T) {
	hooks := &GuardedSendHooks{Admit: func(context.Context, types.MessageID, func(context.Context) error) error { return nil }, Entered: func() {}}
	msg := &waE2E.Message{Conversation: proto.String("hi")}
	before := proto.Clone(msg)
	own := types.NewJID("15550100009", types.DefaultUserServer)
	// A logged-in minimal client reaches the reporting-token metadata write. It fails later
	// without a full store; the caller's message must be unchanged either way.
	cli := &Client{Store: &store.Device{ID: &own}, SendReportingTokens: true}
	func() {
		defer func() { _ = recover() }()
		_, _ = cli.SendMessage(t.Context(), types.NewJID("15550100001", types.DefaultUserServer), msg, SendRequestExtra{ID: "guarded-1", GuardedSend: hooks})
	}()
	if !proto.Equal(msg, before) || !validGuardedText(msg) {
		t.Fatal("a guarded send must not write library metadata into the caller's message")
	}
}

func TestGuardedSendWriteAtDeadlineIsRefused(t *testing.T) {
	previous := guardAdmitTimeout
	guardAdmitTimeout = 20 * time.Millisecond
	t.Cleanup(func() { guardAdmitTimeout = previous })
	for i := 0; i < 50; i++ {
		var sends atomic.Int32
		late := make(chan error, 1)
		hooks := &GuardedSendHooks{
			Admit: func(ctx context.Context, _ types.MessageID, write func(context.Context) error) error {
				<-ctx.Done() // a decision that arrives at the deadline
				late <- write(context.Background())
				return nil
			},
			Entered: func() {},
		}
		_, err := guardedTestClient(&sends, nil).sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
		if !errors.Is(<-late, ErrGuardedSendRefused) || !errors.Is(err, ErrGuardedSendRefused) || sends.Load() != 0 {
			t.Fatalf("iteration %d: a write at or after the deadline must be refused: err=%v sends=%d", i, err, sends.Load())
		}
	}
}

type guardedTestLIDs struct{ resolved types.JID }

func (s guardedTestLIDs) PutManyLIDMappings(context.Context, []store.LIDMapping) error { return nil }
func (s guardedTestLIDs) PutLIDMapping(context.Context, types.JID, types.JID) error    { return nil }
func (s guardedTestLIDs) GetPNForLID(context.Context, types.JID) (types.JID, error) {
	return types.EmptyJID, nil
}
func (s guardedTestLIDs) GetLIDForPN(context.Context, types.JID) (types.JID, error) {
	return s.resolved, nil
}
func (s guardedTestLIDs) GetManyLIDsForPNs(context.Context, []types.JID) (map[types.JID]types.JID, error) {
	return nil, nil
}

// A guarded send must reach only sendDM. A phone number that resolves to anything other than a
// LID user would route to a sender where admission never runs.
func TestSendMessageGuardedRevalidatesResolvedRecipient(t *testing.T) {
	var admitted atomic.Int32
	hooks := &GuardedSendHooks{
		Admit:   func(context.Context, types.MessageID, func(context.Context) error) error { admitted.Add(1); return nil },
		Entered: func() {},
	}
	own := types.NewJID("15550100009", types.DefaultUserServer)
	for _, resolved := range []types.JID{
		types.NewJID("120363000000000000", types.GroupServer),
		types.NewJID("status", types.BroadcastServer),
		types.NewJID("120363000000000001", types.NewsletterServer),
	} {
		var sends atomic.Int32
		cli := &Client{Store: &store.Device{ID: &own, LIDs: guardedTestLIDs{resolved: resolved}}, Log: waLog.Noop}
		cli.testSendHook = func([]byte) error { sends.Add(1); return nil }
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			_, err = cli.SendMessage(t.Context(), types.NewJID("15550100001", types.DefaultUserServer),
				&waE2E.Message{Conversation: proto.String("hi")}, SendRequestExtra{ID: "guarded-1", GuardedSend: hooks})
		}()
		if !errors.Is(err, ErrGuardedSendUnsupported) || sends.Load() != 0 {
			t.Errorf("resolved %s: want ErrGuardedSendUnsupported with nothing sent, got err=%v sends=%d", resolved.Server, err, sends.Load())
		}
	}
	if admitted.Load() != 0 {
		t.Fatal("admission must not run for an ineligible resolved recipient")
	}
}

func TestGuardedSendAdmitIsAClientOwnedTask(t *testing.T) {
	var sends atomic.Int32
	cli := NewClient(&store.Device{}, nil)
	cli.testSendHook = func([]byte) error { sends.Add(1); return nil }
	admitting := make(chan struct{})
	admitCancelled := make(chan struct{})
	hooks := &GuardedSendHooks{
		Admit: func(ctx context.Context, _ types.MessageID, _ func(context.Context) error) error {
			close(admitting)
			<-ctx.Done()
			close(admitCancelled)
			return ctx.Err()
		},
		Entered: func() {},
	}
	sendDone := make(chan error, 1)
	go func() {
		_, err := cli.sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-1", hooks)
		sendDone <- err
	}()
	<-admitting
	// FinalClose cancels and joins the admission goroutine instead of leaving it running.
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("FinalClose: %v", err)
	}
	select {
	case <-admitCancelled:
	default:
		t.Fatal("FinalClose returned before the Admit task observed cancellation")
	}
	if err := <-sendDone; !errors.Is(err, ErrGuardedSendRefused) || sends.Load() != 0 {
		t.Fatalf("a cancelled admission must refuse without writing: err=%v sends=%d", err, sends.Load())
	}

	_, err := cli.sendGuardedMessageNode(t.Context(), guardedTestNode, "guarded-2", hooks)
	if !errors.Is(err, ErrGuardedSendRefused) || !errors.Is(err, ErrClientClosed) {
		t.Fatalf("a guarded send after FinalClose must be refused: %v", err)
	}
}
