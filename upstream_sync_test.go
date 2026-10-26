// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/appstate"
	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	"github.com/polymorfa/hypermeow/util/gcmutil"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

func TestHandlerQueueCancellationWaitsForActiveHandler(t *testing.T) {
	cli := NewClient(&store.Device{}, waLog.Noop)
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cli.nodeHandlers["test"] = func(context.Context, *waBinary.Node) {
		close(started)
		<-release
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	queue := make(chan *waBinary.Node, 1)
	queue <- &waBinary.Node{Tag: "test"}
	go cli.handlerQueueLoop(t.Context(), ctx, queue, finished)
	<-started
	cancel()
	select {
	case <-finished:
		t.Error("queue completed before its active handler")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("queue did not complete after handler exited")
	}
}

func TestRetiredQueueProcessesStreamErrorBeforeReconnect(t *testing.T) {
	cli := NewClient(&store.Device{}, waLog.Noop)
	wait := make(chan struct{})
	cli.handlerQueueWait = wait
	cli.forceAutoReconnect.Store(true)
	finished := make(chan struct{})
	go func() {
		cli.finishDisconnect(t.Context(), true, wait)
		close(finished)
	}()
	select {
	case <-finished:
		t.Error("disconnect evaluated before queue completion")
	case <-time.After(50 * time.Millisecond):
	}
	queue := make(chan *waBinary.Node, 1)
	queue <- &waBinary.Node{Tag: "stream:error", Content: []waBinary.Node{{Tag: "conflict", Attrs: waBinary.Attrs{"type": "replaced"}}}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cli.handlerQueueLoop(t.Context(), ctx, queue, wait)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not finish after queue drained")
	}
	if !cli.isExpectedDisconnect() || cli.forceAutoReconnect.Load() {
		t.Fatal("stream replacement did not suppress reconnect")
	}
}

func TestAppStateSyncErrorHandlerCanRetry(t *testing.T) {
	cli := NewClient(&store.Device{AppState: &store.NoopStore{}}, waLog.Noop)
	called := false
	cli.AddEventHandler(func(evt any) {
		if _, ok := evt.(*events.AppStateSyncError); !ok {
			return
		}
		called = true
		if !cli.appStateSyncLock.TryLock() {
			t.Error("error callback still holds app-state lock")
			return
		}
		cli.appStateSyncLock.Unlock()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := cli.FetchAppState(ctx, appstate.WAPatchRegular, true, false); err == nil {
			t.Error("disconnected retry unexpectedly succeeded")
		}
	})
	if err := cli.FetchAppState(t.Context(), appstate.WAPatchRegular, true, false); err == nil {
		t.Fatal("disconnected full sync unexpectedly succeeded")
	}
	if !called {
		t.Fatal("missing full-sync error callback")
	}
}

func TestQRChannelCoalescesSecretRotations(t *testing.T) {
	cli := NewClient(&store.Device{}, waLog.Noop)
	output := make(chan QRChannelItem, 8)
	qrc := &qrChannel{cli: cli, log: waLog.Noop, ctx: t.Context(), output: output, stopQRs: make(chan struct{}), rotateAdv: make(chan struct{}, 1)}
	oldSecret := "secret-0"
	for i := 1; i <= 10; i++ {
		newSecret := fmt.Sprintf("secret-%d", i)
		qrc.handleEvent(&events.RotateADVSecret{OldSecret: oldSecret, NewSecret: newSecret})
		oldSecret = newSecret
	}
	finished := make(chan struct{})
	go func() {
		qrc.emitQRs([]string{"ref,noise,identity,secret-5,platform"})
		close(finished)
	}()
	defer func() { close(qrc.stopQRs); <-finished }()
	select {
	case item := <-output:
		if item.Code != "ref,noise,identity,secret-10,platform" {
			t.Fatalf("emitted stale QR: %s", item.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("no QR emitted")
	}
	qrc.handleEvent(&events.RotateADVSecret{OldSecret: "secret-10", NewSecret: "secret-11"})
	select {
	case item := <-output:
		if item.Code != "ref,noise,identity,secret-11,platform" {
			t.Fatalf("active QR was not refreshed: %s", item.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("rotation did not refresh active QR")
	}
}

type botEditSecretStore struct {
	store.NoopStore
	secret []byte
}

func (s *botEditSecretStore) GetMessageSecret(context.Context, types.JID, types.JID, types.MessageID) ([]byte, types.JID, error) {
	return s.secret, types.EmptyJID, nil
}

func TestFullBotEditDecryptsWithTargetMessageID(t *testing.T) {
	secret := make([]byte, 32)
	secretStore := &botEditSecretStore{secret: secret}
	cli := NewClient(&store.Device{MsgSecrets: secretStore}, waLog.Noop)
	cli.SynchronousAck = true
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: types.NewMetaAIJID, Sender: types.NewMetaAIJID},
		ID:            "outer-edit",
		MsgBotInfo:    types.MsgBotInfo{EditType: types.EditTypeFull, EditTargetID: "original-bot-message"},
		MsgMetaInfo:   types.MsgMetaInfo{TargetID: "original-user-message", TargetSender: types.NewJID("123", types.HiddenUserServer)},
	}
	key, aad := generateMsgSecretKey("", info.Sender, info.MsgBotInfo.EditTargetID, info.MsgMetaInfo.TargetSender, applyBotMessageHKDF(secret))
	plaintext, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("edited text")})
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, 12)
	ciphertext, err := gcmutil.Encrypt(key, iv, plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := proto.Marshal(&waE2E.MessageSecretMessage{EncIV: iv, EncPayload: ciphertext})
	if err != nil {
		t.Fatal(err)
	}
	received := false
	cli.AddEventHandler(func(evt any) {
		if msg, ok := evt.(*events.Message); ok {
			received = msg.Message.GetConversation() == "edited text"
		}
	})
	cli.decryptMessages(t.Context(), info, &waBinary.Node{Tag: "message", Content: []waBinary.Node{{Tag: "enc", Attrs: waBinary.Attrs{"type": "msmsg", "v": "2"}, Content: payload}}})
	if !received {
		t.Fatal("full bot edit failed to decrypt with target ID")
	}
}
