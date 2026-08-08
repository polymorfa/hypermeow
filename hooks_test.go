// Copyright (c) 2026 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/polymorfa/libsignal-protocol-go/ecc"
	"github.com/polymorfa/libsignal-protocol-go/keys/identity"
	"github.com/polymorfa/libsignal-protocol-go/protocol"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func testClient(t *testing.T) *Client {
	t.Helper()
	return NewClient(&store.Device{}, waLog.Noop)
}

// --- RawNodeHandler ---------------------------------------------------------

func TestRawNodeHandlerGetsTheBytesTheNodeWasDecodedFrom(t *testing.T) {
	// A proxy that forwards stanzas elsewhere wants the bytes that arrived,
	// not a re-encoding: this library and whoever wrote the frame may encode
	// one value differently and both are valid.
	cli := testClient(t)
	node := waBinary.Node{
		Tag:   "receipt",
		Attrs: waBinary.Attrs{"id": "ABCD1234", "type": "read"},
	}
	marshaled, err := waBinary.Marshal(node)
	if err != nil {
		t.Fatalf("failed to marshal fixture: %v", err)
	}

	var got RawNode
	var frameCopy []byte
	cli.RawNodeHandler = func(_ context.Context, raw RawNode) (*waBinary.Node, bool) {
		got = raw
		frameCopy = bytes.Clone(raw.Frame)
		return nil, true
	}
	cli.handleFrame(context.Background(), marshaled)

	if got.Node == nil || got.Node.Tag != "receipt" {
		t.Fatalf("unexpected node: %+v", got.Node)
	}
	// Marshal writes a leading format byte that the decoder consumes, so the
	// frame is everything after it. Pinning this is the point: a consumer
	// replaying the bytes has to know exactly which buffer it was handed.
	if want := marshaled[1:]; !bytes.Equal(frameCopy, want) {
		t.Fatalf("frame is not the decoded buffer:\n got %x\nwant %x", frameCopy, want)
	}

	// And the frame really does decode back to the same stanza.
	unpacked, err := waBinary.Unpack(marshaled)
	if err != nil {
		t.Fatalf("failed to unpack: %v", err)
	}
	if !bytes.Equal(frameCopy, unpacked) {
		t.Fatal("frame is not what Unpack produced")
	}
}

func TestRawNodeHandlerCanStillDropAndReplace(t *testing.T) {
	// The behaviour the signature change must not have disturbed.
	marshaled, err := waBinary.Marshal(waBinary.Node{Tag: "receipt", Attrs: waBinary.Attrs{}})
	if err != nil {
		t.Fatalf("failed to marshal fixture: %v", err)
	}

	cli := testClient(t)
	cli.RawNodeHandler = func(_ context.Context, _ RawNode) (*waBinary.Node, bool) {
		return nil, true
	}
	cli.handleFrame(context.Background(), marshaled)
	if len(cli.handlerQueue) != 0 {
		t.Fatal("a dropped node still reached the dispatch queue")
	}

	replaced := testClient(t)
	replaced.RawNodeHandler = func(_ context.Context, _ RawNode) (*waBinary.Node, bool) {
		return &waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{}}, false
	}
	replaced.handleFrame(context.Background(), marshaled)
	select {
	case queued := <-replaced.handlerQueue:
		if queued.Tag != "iq" {
			t.Fatalf("the replacement did not reach dispatch: %s", queued.Tag)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing reached the dispatch queue")
	}
}

func TestNoRawNodeHandlerCostsNothing(t *testing.T) {
	marshaled, err := waBinary.Marshal(waBinary.Node{Tag: "receipt", Attrs: waBinary.Attrs{}})
	if err != nil {
		t.Fatalf("failed to marshal fixture: %v", err)
	}
	cli := testClient(t)
	cli.handleFrame(context.Background(), marshaled)
	if len(cli.handlerQueue) != 1 {
		t.Fatal("the node did not reach dispatch with no handler set")
	}
}

// --- DecryptedPayloadHandler ------------------------------------------------

// bufferedPlaintext is an [store.EventBuffer] that hands back a plaintext
// without any Signal work.
//
// [Client.bufferedDecrypt] returns a buffered plaintext before calling the
// decrypt closure, which is what makes the decryption path reachable in a test
// at all: standing up a real double-ratchet session pair would take more
// scaffolding than the thing under test.
type bufferedPlaintext struct {
	store.NoopStore
	plaintext []byte
	cleared   int
}

func (b *bufferedPlaintext) GetBufferedEvent(context.Context, [32]byte) (*store.BufferedEvent, error) {
	return &store.BufferedEvent{Plaintext: b.plaintext, InsertTime: time.Now()}, nil
}

func (b *bufferedPlaintext) ClearBufferedEventPlaintext(context.Context, [32]byte) error {
	b.cleared++
	return nil
}

func (b *bufferedPlaintext) DeleteOldBufferedHashes(context.Context) error { return nil }

// signalCiphertext builds a structurally valid SignalMessage.
//
// Never decrypted: the buffered plaintext short-circuits that. It has to parse,
// because `decryptDM` parses before it consults the buffer.
func signalCiphertext(t *testing.T) []byte {
	t.Helper()
	ratchet, err := ecc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate ratchet key: %v", err)
	}
	senderKey, err := ecc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate sender key: %v", err)
	}
	receiverKey, err := ecc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate receiver key: %v", err)
	}

	msg, err := protocol.NewSignalMessage(
		3, 0, 0, make([]byte, 32),
		ratchet.PublicKey(), []byte("ciphertext"),
		identity.NewKey(senderKey.PublicKey()), identity.NewKey(receiverKey.PublicKey()),
		pbSerializer.SignalMessage,
	)
	if err != nil {
		t.Fatalf("failed to build signal message: %v", err)
	}
	return msg.Serialize()
}

// padded appends the padding `unpadMessage` strips for a v2 message.
func padded(plaintext []byte) []byte {
	const pad = 4
	return append(append([]byte{}, plaintext...), bytes.Repeat([]byte{pad}, pad)...)
}

// decryptFixture wires a client whose decryption yields `plaintext`, plus the
// `<message>` stanza to feed it.
func decryptFixture(t *testing.T, plaintext []byte, children ...waBinary.Node) (*Client, *types.MessageInfo, *waBinary.Node) {
	t.Helper()
	cli := testClient(t)
	cli.EnableDecryptedEventBuffer = true
	buffer := &bufferedPlaintext{plaintext: padded(plaintext)}
	cli.Store.EventBuffer = buffer

	node := &waBinary.Node{
		Tag:     "message",
		Attrs:   waBinary.Attrs{"id": "ABCD1234"},
		Content: children,
	}
	info := &types.MessageInfo{
		ID: "ABCD1234",
		MessageSource: types.MessageSource{
			// A `@lid` sender skips the PN-to-LID migration lookup, which
			// would need a store this test does not stand up.
			Sender: types.JID{User: "1234", Server: types.HiddenUserServer},
			Chat:   types.JID{User: "1234", Server: types.HiddenUserServer},
		},
		Timestamp: time.Now(),
	}
	return cli, info, node
}

func encNode(t *testing.T, encType string) waBinary.Node {
	t.Helper()
	return waBinary.Node{
		Tag:     "enc",
		Attrs:   waBinary.Attrs{"type": encType, "v": "2"},
		Content: signalCiphertext(t),
	}
}

func TestDecryptedPayloadHandlerSeesEveryPlaintext(t *testing.T) {
	cli, info, node := decryptFixture(t, []byte("not-a-protobuf"), encNode(t, "msg"))

	var seen []DecryptedPayload
	cli.DecryptedPayloadHandler = func(_ context.Context, payload DecryptedPayload) {
		clone := payload
		clone.Plaintext = bytes.Clone(payload.Plaintext)
		seen = append(seen, clone)
	}
	cli.decryptMessages(context.Background(), info, node)

	if len(seen) != 1 {
		t.Fatalf("expected one payload, got %d", len(seen))
	}
	got := seen[0]
	if !bytes.Equal(got.Plaintext, []byte("not-a-protobuf")) {
		t.Fatalf("plaintext is not what decryption produced: %q", got.Plaintext)
	}
	if got.EncType != "msg" {
		t.Fatalf("unexpected enc type: %q", got.EncType)
	}
	if got.ChildIndex != 0 {
		t.Fatalf("unexpected child index: %d", got.ChildIndex)
	}
	if got.Info == nil || got.Info.ID != "ABCD1234" {
		t.Fatal("the payload does not carry its message's info")
	}
	if got.Node == nil || got.Node.Tag != "message" {
		t.Fatal("the payload does not carry its stanza")
	}
}

func TestDecryptedPayloadHandlerFiresEvenWhenTheLibraryCannotReadThePlaintext(t *testing.T) {
	// The reason the hook exists. `not-a-protobuf` fails `proto.Unmarshal`
	// below the hook, and before this the plaintext went nowhere: the ratchet
	// had already advanced, so nobody could ever get those bytes again.
	cli, info, node := decryptFixture(t, []byte("not-a-protobuf"), encNode(t, "msg"))

	var fired bool
	cli.DecryptedPayloadHandler = func(_ context.Context, payload DecryptedPayload) {
		fired = true
		if !bytes.Equal(payload.Plaintext, []byte("not-a-protobuf")) {
			t.Errorf("unexpected plaintext: %q", payload.Plaintext)
		}
	}
	cli.decryptMessages(context.Background(), info, node)

	if !fired {
		t.Fatal("a plaintext the library could not interpret was dropped without anyone seeing it")
	}
}

func TestChildIndexAddressesTheEncItCameFrom(t *testing.T) {
	// Counting `<enc>` nodes separately would be ambiguous the moment a
	// stanza carries anything else, and real ones do.
	cli, info, node := decryptFixture(
		t,
		[]byte("not-a-protobuf"),
		waBinary.Node{Tag: "participants", Attrs: waBinary.Attrs{}},
		encNode(t, "msg"),
		waBinary.Node{Tag: "device-identity", Attrs: waBinary.Attrs{}},
		encNode(t, "msg"),
	)

	var indices []int
	cli.DecryptedPayloadHandler = func(_ context.Context, payload DecryptedPayload) {
		indices = append(indices, payload.ChildIndex)
		children, ok := payload.Node.Content.([]waBinary.Node)
		if !ok {
			t.Error("the stanza does not carry its children")
			return
		}
		if payload.ChildIndex >= len(children) || children[payload.ChildIndex].Tag != "enc" {
			t.Errorf("child index %d does not address an <enc>", payload.ChildIndex)
		}
	}
	cli.decryptMessages(context.Background(), info, node)

	if len(indices) != 2 {
		t.Fatalf("expected two payloads, got %d", len(indices))
	}
	if indices[0] != 1 || indices[1] != 3 {
		t.Fatalf("indices do not address the <enc> children: %v", indices)
	}
}

func TestNoDecryptedPayloadHandlerIsHarmless(t *testing.T) {
	cli, info, node := decryptFixture(t, []byte("not-a-protobuf"), encNode(t, "msg"))
	cli.decryptMessages(context.Background(), info, node)
}
