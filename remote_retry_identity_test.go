package whatsmeow

import (
	"bytes"
	"context"
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
)

// Authored public-key envelope: these opaque identity bytes are not a live
// packet or a verified signature. Verification belongs to the remote store.
func authoredRemoteBundleNode(jid types.JID, identity []byte) waBinary.Node {
	return waBinary.Node{Tag: "user", Attrs: waBinary.Attrs{"jid": jid}, Content: []waBinary.Node{
		{Tag: "registration", Content: []byte{0, 0, 0, 1}},
		{Tag: "identity", Content: testRemoteIdentity.IdentityKey[:]},
		{Tag: "skey", Content: []waBinary.Node{
			{Tag: "id", Content: []byte{0, 0, 1}},
			{Tag: "value", Content: testRemoteIdentity.SignedPreKey[:]},
			{Tag: "signature", Content: testRemoteIdentity.SignedPreKeySignature[:]},
		}},
		{Tag: "device-identity", Content: identity},
	}}
}

func TestRemoteRetryPreservesCompanionIdentity(t *testing.T) {
	cli, remote, _ := newTestRemote(t)
	requester := types.JID{User: "200000000002", Device: 2, Server: types.HiddenUserServer}
	remote.resendWith = marshalStanza(t, waBinary.Node{Tag: "message", Attrs: waBinary.Attrs{"id": "authored-retry", "to": requester, "type": "text"}})
	identity := []byte("authored retry identity")
	bundleNode := authoredRemoteBundleNode(requester, identity)
	bundle := bundleNode.GetChildren()
	receipt := &events.Receipt{MessageSource: types.MessageSource{Chat: requester.ToNonAD(), Sender: requester}}
	node := &waBinary.Node{Tag: "receipt", Attrs: waBinary.Attrs{"id": "authored-retry", "from": requester, "type": "retry"}, Content: []waBinary.Node{
		{Tag: "retry", Attrs: waBinary.Attrs{"id": "authored-retry", "t": "1700000000", "count": "1"}},
		bundle[0], {Tag: "keys", Content: bundle[1:]},
	}}
	if err := cli.handleRetryReceipt(context.Background(), receipt, node); err != nil {
		t.Fatal(err)
	}
	if len(remote.resends) != 1 || remote.resends[0].Bundle == nil || !bytes.Equal(remote.resends[0].DeviceIdentity, identity) {
		t.Fatal("lost retry companion identity")
	}
}
