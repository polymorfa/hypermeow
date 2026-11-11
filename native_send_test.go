package whatsmeow

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/types"
)

func nativeTestClient(sends *atomic.Int32, sendErr error) *Client {
	return &Client{testSendHook: func([]byte) error { sends.Add(1); return sendErr }}
}

var nativeTestNode = waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": "native-1"}}

func TestNativeSendNilHooksKeepsExistingPath(t *testing.T) {
	var sends atomic.Int32
	data, err := nativeTestClient(&sends, nil).sendNativeMessageNode(t.Context(), nativeTestNode, "native-1", nil)
	if err != nil || len(data) == 0 || sends.Load() != 1 {
		t.Fatalf("nil hooks must use the existing sender: err=%v sends=%d", err, sends.Load())
	}
}

func TestNativeSendRefusalWritesNothing(t *testing.T) {
	var sends atomic.Int32
	entered := false
	hooks := &NativeSendHooks{
		Admit:   func(context.Context, types.MessageID, func(context.Context) error) error { return errors.New("authority lost") },
		Entered: func() { entered = true },
	}
	_, err := nativeTestClient(&sends, nil).sendNativeMessageNode(t.Context(), nativeTestNode, "native-1", hooks)
	if !errors.Is(err, ErrNativeSendRefused) || errors.Is(err, ErrNativeSendUncertain) || entered || sends.Load() != 0 {
		t.Fatalf("refusal must write nothing: err=%v entered=%v sends=%d", err, entered, sends.Load())
	}
}

func TestNativeSendAdmittedWritesOnceAfterEntered(t *testing.T) {
	var sends atomic.Int32
	var order []string
	cli := &Client{testSendHook: func([]byte) error { order = append(order, "send"); sends.Add(1); return nil }}
	hooks := &NativeSendHooks{
		Admit: func(ctx context.Context, id types.MessageID, write func(context.Context) error) error {
			if id != "native-1" {
				t.Errorf("unexpected id %q", id)
			}
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > NativeAdmitTimeout {
				t.Error("Admit must run under the bounded admission deadline")
			}
			if err := write(context.Background()); err != nil {
				return err
			}
			if !errors.Is(write(context.Background()), ErrNativeSendRefused) {
				t.Error("a second write must be refused")
			}
			return nil
		},
		Entered: func() { order = append(order, "entered") },
	}
	data, err := cli.sendNativeMessageNode(t.Context(), nativeTestNode, "native-1", hooks)
	if err != nil || len(data) == 0 || sends.Load() != 1 || len(order) != 2 || order[0] != "entered" || order[1] != "send" {
		t.Fatalf("admitted send: err=%v sends=%d order=%v", err, sends.Load(), order)
	}
}

func TestNativeSendRetainedWriteIsClosed(t *testing.T) {
	var sends atomic.Int32
	var retained func(context.Context) error
	hooks := &NativeSendHooks{
		Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
			retained = write
			return nil
		},
		Entered: func() {},
	}
	_, err := nativeTestClient(&sends, nil).sendNativeMessageNode(t.Context(), nativeTestNode, "native-1", hooks)
	if !errors.Is(err, ErrNativeSendRefused) {
		t.Fatalf("returning without writing must refuse: %v", err)
	}
	if !errors.Is(retained(context.Background()), ErrNativeSendRefused) || sends.Load() != 0 {
		t.Fatalf("a retained write after Admit returned must not send: sends=%d", sends.Load())
	}
}

// A write racing Admit's return either refuses or completes before the wrapper returns.
// No write may start or still be running after sendNativeMessageNode returns.
func TestNativeSendConcurrentReturnHasNoGap(t *testing.T) {
	for i := 0; i < 500; i++ {
		var sends atomic.Int32
		cli := nativeTestClient(&sends, nil)
		var wg sync.WaitGroup
		hooks := &NativeSendHooks{
			Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
				wg.Add(1)
				go func() { defer wg.Done(); _ = write(context.Background()) }()
				return nil
			},
			Entered: func() {},
		}
		_, err := cli.sendNativeMessageNode(t.Context(), nativeTestNode, "native-1", hooks)
		afterReturn := sends.Load()
		wg.Wait()
		if sends.Load() != afterReturn {
			t.Fatalf("iteration %d: a write ran after the wrapper returned", i)
		}
		if (afterReturn == 0) != errors.Is(err, ErrNativeSendRefused) || afterReturn > 1 {
			t.Fatalf("iteration %d: sends=%d err=%v", i, afterReturn, err)
		}
	}
}

func TestNativeSendFailureAfterHandoffIsUncertain(t *testing.T) {
	var sends atomic.Int32
	hooks := &NativeSendHooks{
		Admit: func(_ context.Context, _ types.MessageID, write func(context.Context) error) error {
			return write(context.Background())
		},
		Entered: func() {},
	}
	_, err := nativeTestClient(&sends, errors.New("socket lost")).sendNativeMessageNode(t.Context(), nativeTestNode, "native-1", hooks)
	if !errors.Is(err, ErrNativeSendUncertain) || errors.Is(err, ErrNativeSendRefused) || sends.Load() != 1 {
		t.Fatalf("a failure after handoff must be uncertain, never refused: err=%v", err)
	}
}

func TestNativeSendRequiresHooksAndID(t *testing.T) {
	var sends atomic.Int32
	cli := nativeTestClient(&sends, nil)
	for _, hooks := range []*NativeSendHooks{{Entered: func() {}}, {Admit: func(context.Context, types.MessageID, func(context.Context) error) error { return nil }}} {
		if _, err := cli.sendNativeMessageNode(t.Context(), nativeTestNode, "native-1", hooks); !errors.Is(err, ErrNativeSendUnsupported) {
			t.Fatalf("incomplete hooks must be unsupported: %v", err)
		}
	}
	full := &NativeSendHooks{Admit: func(context.Context, types.MessageID, func(context.Context) error) error { return nil }, Entered: func() {}}
	if _, err := cli.sendNativeMessageNode(t.Context(), nativeTestNode, "", full); !errors.Is(err, ErrNativeSendUnsupported) {
		t.Fatalf("an empty ID must be unsupported: %v", err)
	}
	if _, err := (&Client{}).sendNativeMessageNode(t.Context(), nativeTestNode, "native-1", full); !errors.Is(err, ErrNotConnected) || sends.Load() != 0 {
		t.Fatalf("no socket must refuse before Admit: %v", err)
	}
}

func TestSendMessageRefusesUnsupportedNativeSends(t *testing.T) {
	hooks := &NativeSendHooks{Admit: func(context.Context, types.MessageID, func(context.Context) error) error { return nil }, Entered: func() {}}
	text := &waE2E.Message{Conversation: proto.String("hi")}
	user := types.NewJID("15550100001", types.DefaultUserServer)
	group := types.NewJID("120363000000000000", types.GroupServer)
	for name, tc := range map[string]struct {
		to    types.JID
		msg   *waE2E.Message
		extra SendRequestExtra
	}{
		"no id":     {user, text, SendRequestExtra{NativeSend: hooks}},
		"group":     {group, text, SendRequestExtra{ID: "native-1", NativeSend: hooks}},
		"peer":      {user, text, SendRequestExtra{ID: "native-1", NativeSend: hooks, Peer: true}},
		"media":     {user, &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, SendRequestExtra{ID: "native-1", NativeSend: hooks}},
		"extra":     {user, &waE2E.Message{Conversation: proto.String("hi"), ImageMessage: &waE2E.ImageMessage{}}, SendRequestExtra{ID: "native-1", NativeSend: hooks}},
		"meta node": {user, text, SendRequestExtra{ID: "native-1", NativeSend: hooks, Meta: &types.MsgMetaInfo{}}},
	} {
		if _, err := (&Client{}).SendMessage(t.Context(), tc.to, tc.msg, tc.extra); !errors.Is(err, ErrNativeSendUnsupported) {
			t.Errorf("%s: want ErrNativeSendUnsupported, got %v", name, err)
		}
	}
}
