// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"testing"

	"github.com/polymorfa/libsignal-protocol-go/session"
	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

// TestPrepareMessageNodeHostedFanout checks send.go's device filtering: a
// hosted device 99 receives 1:1 messages but is dropped from group fanout.
// Sessions are established in memory so no prekey IQ is needed.
func TestPrepareMessageNodeHostedFanout(t *testing.T) {
	ownPN := types.JID{User: "15550000000", Device: 7, Server: types.DefaultUserServer}
	business := types.NewJID("15551234567", types.DefaultUserServer)
	businessLID := types.NewJID("123456789012345", types.HiddenUserServer)
	companion, _ := newSignalTestDevice(ownPN, &lidStub{pn: business, lid: businessLID})
	cli := NewClient(companion, waLog.Noop)

	primary := types.JID{User: business.User, Server: types.DefaultUserServer}
	hosted := types.JID{User: business.User, Device: HostedDeviceID, Server: types.HostedServer}
	ensureMap(&cli.userDevicesCache)[business] = deviceCache{devices: []types.JID{primary, hosted}}

	// Outgoing sessions under the addresses the send path derives. The stub
	// has no batch PN->LID lookup, so the primary stays on its PN address.
	for _, peer := range []types.JID{
		primary,
		{User: businessLID.User, Device: HostedDeviceID, Server: types.HostedLIDServer},
	} {
		peerDevice, _ := newSignalTestDevice(peer, &lidStub{})
		builder := session.NewBuilderFromSignal(companion, peer.SignalAddress(), pbSerializer)
		if err := builder.ProcessBundle(context.Background(), signalTestBundle(t, peerDevice, uint32(peer.Device))); err != nil {
			t.Fatal(err)
		}
	}

	message := &waE2E.Message{Conversation: proto.String("hello")}
	plaintext, dsmPlaintext, err := marshalMessage(business, message)
	if err != nil {
		t.Fatal(err)
	}
	participantJIDs := func(node *waBinary.Node) []types.JID {
		var out []types.JID
		participants := node.GetChildByTag("participants")
		for _, to := range participants.GetChildren() {
			out = append(out, to.AttrGetter().JID("jid"))
		}
		return out
	}

	node, devices, err := cli.prepareMessageNode(context.Background(), business, "3EB0DM", message, []types.JID{business}, plaintext, dsmPlaintext, &MessageDebugTimings{}, nodeExtraParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[1] != hosted {
		t.Fatalf("1:1 devices = %v, want primary and %s", devices, hosted)
	}
	if got := participantJIDs(node); len(got) != 2 || got[1] != hosted {
		t.Fatalf("1:1 participants = %v, want primary and %s", got, hosted)
	}

	group := types.NewJID("120363000000000000", types.GroupServer)
	node, devices, err = cli.prepareMessageNode(context.Background(), group, "3EB0GRP", message, []types.JID{business}, plaintext, nil, &MessageDebugTimings{}, nodeExtraParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0] != primary {
		t.Fatalf("group devices = %v, want only %s", devices, primary)
	}
	if got := participantJIDs(node); len(got) != 1 || got[0] != primary {
		t.Fatalf("group participants = %v, want only %s", got, primary)
	}
}
