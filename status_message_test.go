package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types/events"
	waLog "github.com/polymorfa/hypermeow/util/log"
	"github.com/polymorfa/libsignal-protocol-go/protocol"
	"github.com/polymorfa/libsignal-protocol-go/session"
	"google.golang.org/protobuf/proto"
	"testing"

	"github.com/polymorfa/hypermeow/types"
)

// These envelopes are authored fixtures, not captured WhatsApp traffic.
func TestStatusStanzaReachesEncryptedReceiver(t *testing.T) {
	for _, chat := range []types.JID{types.StatusBroadcastJID, types.NewJID("120363000000001", types.GroupServer)} {
		t.Run(chat.Server, func(t *testing.T) {
			cli, remote, sent := newTestRemote(t)
			own := types.NewJID("100000000001", types.HiddenUserServer)
			cli.Store.ID = &own
			sender := types.JID{User: "200000000002", Server: types.HiddenUserServer, Device: 2}
			_, node := encMessage("pkmsg")
			node.Tag = "status"
			node.Attrs["from"], node.Attrs["participant"] = chat, sender
			cli.nodeHandlers["status"](context.Background(), node)
			if len(remote.receives) != 1 {
				t.Fatalf("encrypted receiver calls = %d, want 1", len(remote.receives))
			}
			req := remote.receives[0]
			if req.Chat != chat || req.Sender != sender {
				t.Fatalf("routing changed: %+v", req)
			}
			if len(sent.nodes) == 0 {
				t.Fatal("status was neither acknowledged nor receipted")
			}
			if node.Tag != "status" {
				t.Fatal("handler changed the original transport tag")
			}
		})
	}
}

func TestStatusSessionsKeepChatRatchetsSeparate(t *testing.T) {
	ctx := context.Background()
	own := types.JID{User: "100000000001", Server: types.HiddenUserServer, Device: 1}
	sender := types.JID{User: "200000000002", Server: types.HiddenUserServer, Device: 2}
	companion, mem := newSignalTestDevice(own, &store.NoopStore{})
	companion.LID = own
	peer, _ := newSignalTestDevice(sender, &store.NoopStore{})
	cli := NewClient(companion, waLog.Noop)
	cli.SynchronousAck = true
	sent := &sentNodes{}
	cli.testSendHook = sent.hook
	var messages []*events.Message
	cli.AddEventHandler(func(evt any) {
		if e, ok := evt.(*events.Message); ok {
			messages = append(messages, e)
		}
	})
	for _, status := range []bool{false, true, false, true} {
		cryptoCtx := ctx
		if status {
			cryptoCtx = store.WithStatusSession(ctx)
		}
		builder := session.NewBuilderFromSignal(peer, own.SignalAddress(), pbSerializer)
		exists, err := peer.ContainsSession(cryptoCtx, own.SignalAddress())
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			if err = builder.ProcessBundle(cryptoCtx, signalTestBundle(t, companion, uint32(own.Device))); err != nil {
				t.Fatal(err)
			}
		}
		cipher := session.NewCipher(builder, own.SignalAddress())
		text := fmt.Sprintf("synthetic %t %d", status, len(messages))
		plain, err := proto.Marshal(&waE2E.Message{Conversation: proto.String(text)})
		if err != nil {
			t.Fatal(err)
		}
		encrypted, err := cipher.Encrypt(cryptoCtx, padMessage(plain))
		if err != nil {
			t.Fatal(err)
		}
		kind := "msg"
		if encrypted.Type() == protocol.PREKEY_TYPE {
			kind = "pkmsg"
		}
		node := &waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": text, "from": sender, "t": "1700000000", "type": "text"}, Content: []waBinary.Node{{Tag: "enc", Attrs: waBinary.Attrs{"type": kind, "v": "2"}, Content: encrypted.Serialize()}}}
		if status {
			node.Tag = "status"
			node.Attrs["from"], node.Attrs["participant"] = types.StatusBroadcastJID, sender
			node.Content = append(node.GetChildren(), waBinary.Node{Tag: "meta", Attrs: waBinary.Attrs{"session_scope": "status"}})
			cli.handleStatusMessage(ctx, node)
		} else {
			cli.handleEncryptedMessage(ctx, node)
		}
		if len(messages) == 0 || messages[len(messages)-1].Message.GetConversation() != text {
			t.Fatalf("content not delivered for status=%t", status)
		}
		if got := messages[len(messages)-1].Info.StatusKind(); (got == types.StatusKindPersonal) != status {
			t.Fatalf("wrong publication kind %q", got)
		}
	}
	for _, key := range []string{sender.SignalAddress().String(), sender.SignalAddress().String() + "_status"} {
		if _, ok := mem.sessions[key]; !ok {
			t.Fatalf("missing independent session %s", key)
		}
	}
	if len(sent.nodes) != 4 {
		t.Fatalf("receipts = %d", len(sent.nodes))
	}
	for i, n := range sent.nodes {
		if n.Tag != "receipt" {
			t.Fatalf("response %d = %s", i, n.Tag)
		}
		if got := n.Attrs["class"]; (got == "status") != (i%2 == 1) {
			t.Fatalf("response %d class = %v", i, got)
		}
	}
}

func TestStatusSessionModeAndPublicationContext(t *testing.T) {
	for _, mode := range []types.StatusSessionMode{types.StatusSessionDisabled, types.StatusSessionReceiverEnabled, types.StatusSessionSenderEnabled, types.StatusSessionForced} {
		cli := &Client{GetStatusSessionMode: func() types.StatusSessionMode { return mode }}
		for _, scope := range []string{"", "status"} {
			for _, chat := range []types.JID{types.StatusBroadcastJID, types.NewJID("group", types.GroupServer), types.NewJID("sender", types.HiddenUserServer)} {
				info := &types.MessageInfo{MessageSource: types.MessageSource{Chat: chat}, IsGroupStatus: chat.Server == types.GroupServer, MsgMetaInfo: types.MsgMetaInfo{SessionScope: scope}}
				want := chat == types.StatusBroadcastJID || chat.Server == types.GroupServer && scope == "status"
				want = want && (mode == types.StatusSessionForced || mode != types.StatusSessionDisabled && scope == "status")
				if cli.usesStatusSession(info) != want {
					t.Fatalf("mode=%d chat=%s scope=%s", mode, chat, scope)
				}
			}
		}
	}
}

func TestStatusRemoteMetadataAndOutage(t *testing.T) {
	for _, outage := range []bool{false, true} {
		cli, remote, sent := newTestRemote(t)
		own := types.NewJID("100000000001", types.HiddenUserServer)
		cli.Store.ID = &own
		_, node := encMessage("pkmsg")
		node.Tag = "status"
		node.Attrs["from"] = types.NewJID("120363000000001", types.GroupServer)
		node.Attrs["participant"] = types.NewJID("200000000002", types.HiddenUserServer)
		node.Content = append(node.GetChildren(), waBinary.Node{Tag: "meta", Attrs: waBinary.Attrs{"session_scope": "status"}})
		if outage {
			remote.receiveErr = errors.New("synthetic remote outage")
		}
		cli.handleStatusMessage(context.Background(), node)
		if len(remote.receives) != 1 || remote.receives[0].StatusKind != types.StatusKindGroup || !remote.receives[0].StatusSession {
			t.Fatalf("lost remote status metadata: %+v", remote.receives)
		}
		if outage && len(sent.nodes) != 0 {
			t.Fatal("acknowledged status before remote receiver accepted it")
		}
	}
}

func TestStatusRejectsDirectSendersAndMissingParticipants(t *testing.T) {
	for _, missingParticipant := range []bool{false, true} {
		cli, remote, sent := newTestRemote(t)
		own := types.NewJID("100000000001", types.HiddenUserServer)
		cli.Store.ID = &own
		_, node := encMessage("pkmsg")
		node.Tag = "status"
		if missingParticipant {
			node.Attrs["from"] = types.StatusBroadcastJID
		}
		cli.handleStatusMessage(context.Background(), node)
		if len(remote.receives) != 0 {
			t.Fatal("malformed status reached decryption")
		}
		if len(sent.nodes) != 1 || sent.nodes[0].Tag != "ack" || sent.nodes[0].Attrs["class"] != "status" || sent.nodes[0].AttrGetter().Int("error") != NackParsingError {
			t.Fatalf("wrong parse failure response: %+v", sent.nodes)
		}
	}
}

func TestChannelStatusStaysPlaintextAndOrdinaryChannelMessagesStayMessages(t *testing.T) {
	for _, status := range []bool{false, true} {
		cli, remote, _ := newTestRemote(t)
		own := types.NewJID("100000000001", types.HiddenUserServer)
		cli.Store.ID = &own
		data, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("synthetic channel status")})
		if err != nil {
			t.Fatal(err)
		}
		node := &waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": "channel", "from": types.NewJID("120363000000001", types.NewsletterServer), "t": "1700000000"}, Content: []waBinary.Node{{Tag: "plaintext", Content: data}}}
		var got *events.Message
		cli.AddEventHandler(func(evt any) {
			if e, ok := evt.(*events.Message); ok {
				got = e
			}
		})
		if status {
			node.Tag = "status"
			cli.handleStatusMessage(context.Background(), node)
		} else {
			cli.handleEncryptedMessage(context.Background(), node)
		}
		if len(remote.receives) != 0 || got == nil || got.Message.GetConversation() != "synthetic channel status" {
			t.Fatal("channel content did not stay on the plaintext path")
		}
		if (got.Info.StatusKind() == types.StatusKindChannel) != status {
			t.Fatalf("channel publication misclassified: %+v", got.Info)
		}
	}
}

func TestGroupStatusWrapperIsNotAMentionOrReply(t *testing.T) {
	content := &waE2E.Message{Conversation: proto.String("synthetic group status")}
	for _, raw := range []*waE2E.Message{
		{GroupStatusMessageV2: &waE2E.FutureProofMessage{Message: content}},
		{GroupStatusMentionMessage: &waE2E.FutureProofMessage{Message: content}},
		{StatusMentionMessage: &waE2E.FutureProofMessage{Message: content}},
	} {
		evt := (&events.Message{RawMessage: raw}).UnwrapRaw()
		isPublication := raw.GetGroupStatusMessageV2() != nil
		if (evt.Info.StatusKind() == types.StatusKindGroup) != isPublication {
			t.Fatal("mention classified as a publication")
		}
		if isPublication && evt.Message != content {
			t.Fatal("group status content not unwrapped")
		}
	}
}

func TestStatusReceiptPreservesClass(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	own := types.NewJID("100000000001", types.HiddenUserServer)
	cli.Store.ID = &own
	for _, class := range []string{"", "status"} {
		node := waBinary.Node{Tag: "receipt", Attrs: waBinary.Attrs{"from": types.NewJID("123456789", types.HiddenUserServer), "id": "authored-receipt", "t": "1700000000", "class": class}}
		receipt, _, err := cli.parseReceipt(&node)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Class != class {
			t.Fatalf("class = %q, want %q", receipt.Class, class)
		}
	}
}
