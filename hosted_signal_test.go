// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/polymorfa/libsignal-protocol-go/ecc"
	"github.com/polymorfa/libsignal-protocol-go/keys/identity"
	"github.com/polymorfa/libsignal-protocol-go/keys/prekey"
	"github.com/polymorfa/libsignal-protocol-go/protocol"
	"github.com/polymorfa/libsignal-protocol-go/session"
	"github.com/polymorfa/libsignal-protocol-go/util/optional"
	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	"github.com/polymorfa/hypermeow/util/keys"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

// memSignalStore keeps the Signal state needed for a real prekey exchange in memory.
type memSignalStore struct {
	store.NoopStore
	lock       sync.Mutex
	sessions   map[string][]byte
	identities map[string][32]byte
	preKeys    map[uint32]*keys.PreKey
	nextPreKey uint32
}

func newMemSignalStore() *memSignalStore {
	return &memSignalStore{
		sessions:   make(map[string][]byte),
		identities: make(map[string][32]byte),
		preKeys:    make(map[uint32]*keys.PreKey),
		nextPreKey: 1,
	}
}

func (m *memSignalStore) MigratePNToLID(_ context.Context, pn, lid types.JID) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	prefix := pn.SignalAddressUser() + ":"
	replacement := lid.SignalAddressUser() + ":"
	for address, record := range m.sessions {
		if strings.HasPrefix(address, prefix) {
			m.sessions[replacement+strings.TrimPrefix(address, prefix)] = record
			delete(m.sessions, address)
		}
	}
	for address, identity := range m.identities {
		if strings.HasPrefix(address, prefix) {
			m.identities[replacement+strings.TrimPrefix(address, prefix)] = identity
			delete(m.identities, address)
		}
	}
	return nil
}

func (m *memSignalStore) PutIdentity(_ context.Context, address string, key [32]byte) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.identities[address] = key
	return nil
}

func (m *memSignalStore) IsTrustedIdentity(context.Context, string, [32]byte) (bool, error) {
	return true, nil
}

func (m *memSignalStore) GetSession(_ context.Context, address string) ([]byte, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.sessions[address], nil
}

func (m *memSignalStore) HasSession(_ context.Context, address string) (bool, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	_, ok := m.sessions[address]
	return ok, nil
}

func (m *memSignalStore) GetManySessions(_ context.Context, addresses []string) (map[string][]byte, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	out := make(map[string][]byte, len(addresses))
	for _, addr := range addresses {
		out[addr] = m.sessions[addr]
	}
	return out, nil
}

func (m *memSignalStore) PutSession(_ context.Context, address string, sess []byte) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.sessions[address] = sess
	return nil
}

func (m *memSignalStore) PutManySessions(_ context.Context, sessions map[string][]byte) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	for addr, sess := range sessions {
		m.sessions[addr] = sess
	}
	return nil
}

func (m *memSignalStore) DeleteSession(_ context.Context, address string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.sessions, address)
	return nil
}

func (m *memSignalStore) GenOnePreKey(context.Context) (*keys.PreKey, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	key := keys.NewPreKey(m.nextPreKey)
	m.preKeys[key.KeyID] = key
	m.nextPreKey++
	return key, nil
}

func (m *memSignalStore) GetPreKey(_ context.Context, id uint32) (*keys.PreKey, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.preKeys[id], nil
}

func (m *memSignalStore) RemovePreKey(_ context.Context, id uint32) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.preKeys, id)
	return nil
}

func (m *memSignalStore) sessionAddresses() []string {
	m.lock.Lock()
	defer m.lock.Unlock()
	out := make([]string, 0, len(m.sessions))
	for addr := range m.sessions {
		out = append(out, addr)
	}
	slices.Sort(out)
	return out
}

// lidStub maps one PN user to one LID user.
type lidStub struct {
	store.NoopStore
	pn, lid types.JID
}

func (l *lidStub) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	if pn.User == l.pn.User {
		return types.JID{User: l.lid.User, Device: pn.Device, Server: types.HiddenUserServer}, nil
	}
	return types.EmptyJID, nil
}

func (l *lidStub) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	if lid.User == l.lid.User {
		return types.JID{User: l.pn.User, Device: lid.Device, Server: types.DefaultUserServer}, nil
	}
	return types.EmptyJID, nil
}

func newSignalTestDevice(id types.JID, lids store.LIDStore) (*store.Device, *memSignalStore) {
	identityKey := keys.NewKeyPair()
	mem := newMemSignalStore()
	device := &store.Device{
		NoiseKey:       keys.NewKeyPair(),
		IdentityKey:    identityKey,
		SignedPreKey:   identityKey.CreateSignedPreKey(1),
		RegistrationID: 4321,
		ID:             &id,
		LIDs:           lids,
	}
	device.SetAllStores(mem)
	return device, mem
}

func signalTestBundle(t *testing.T, device *store.Device, deviceID uint32) *prekey.Bundle {
	t.Helper()
	preKey, err := device.PreKeys.GenOnePreKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return prekey.NewBundle(device.RegistrationID, deviceID,
		optional.NewOptionalUint32(preKey.KeyID), device.SignedPreKey.KeyID,
		ecc.NewDjbECPublicKey(*preKey.Pub), ecc.NewDjbECPublicKey(*device.SignedPreKey.Pub), *device.SignedPreKey.Signature,
		identity.NewKey(ecc.NewDjbECPublicKey(*device.IdentityKey.Pub)))
}

type signalTestPeer struct {
	device  *store.Device
	builder *session.Builder
	cipher  *session.Cipher
}

// newSignalTestPeer starts a Signal session from a remote device to the companion.
func newSignalTestPeer(t *testing.T, companion *store.Device, peerID types.JID) *signalTestPeer {
	t.Helper()
	device, _ := newSignalTestDevice(peerID, &store.NoopStore{})
	companionAddr := companion.ID.SignalAddress()
	builder := session.NewBuilderFromSignal(device, companionAddr, pbSerializer)
	if err := builder.ProcessBundle(context.Background(), signalTestBundle(t, companion, uint32(companion.ID.Device))); err != nil {
		t.Fatal(err)
	}
	return &signalTestPeer{device: device, builder: builder, cipher: session.NewCipher(builder, companionAddr)}
}

func (p *signalTestPeer) encrypt(t *testing.T, text string) (string, []byte) {
	t.Helper()
	plaintext, err := proto.Marshal(&waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := p.cipher.Encrypt(context.Background(), padMessage(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	encType := "msg"
	if ciphertext.Type() == protocol.PREKEY_TYPE {
		encType = "pkmsg"
	}
	return encType, ciphertext.Serialize()
}

func deliverSignalTestMessage(t *testing.T, cli *Client, from types.JID, id, encType string, content []byte) *types.MessageInfo {
	t.Helper()
	node := &waBinary.Node{
		Tag:   "message",
		Attrs: waBinary.Attrs{"from": from, "id": id, "t": "1700000000", "type": "text"},
		Content: []waBinary.Node{{
			Tag:     "enc",
			Attrs:   waBinary.Attrs{"type": encType, "v": "2"},
			Content: content,
		}},
	}
	info, err := cli.parseMessageInfo(node)
	if err != nil {
		t.Fatal(err)
	}
	cli.decryptMessages(context.Background(), info, node)
	return info
}

func TestHostedSenderDecryptsUnderHostedSignalAddress(t *testing.T) {
	ownPN := types.JID{User: "15550000000", Device: 7, Server: types.DefaultUserServer}
	ownLID := types.JID{User: "100000000000000", Device: 7, Server: types.HiddenUserServer}
	business := types.NewJID("15551234567", types.DefaultUserServer)
	businessLID := types.NewJID("123456789012345", types.HiddenUserServer)
	lids := &lidStub{pn: business, lid: businessLID}
	companion, mem := newSignalTestDevice(ownPN, lids)
	companion.LID = ownLID
	cli := NewClient(companion, waLog.Noop)
	cli.SynchronousAck = true
	sent := &sentNodes{}
	cli.testSendHook = sent.hook

	var got []string
	var undecryptable int
	cli.AddEventHandler(func(evt any) {
		switch e := evt.(type) {
		case *events.Message:
			got = append(got, e.Message.GetConversation())
		case *events.UndecryptableMessage:
			undecryptable++
		}
	})

	hostedLID := types.JID{User: businessLID.User, Device: HostedDeviceID, Server: types.HostedLIDServer}
	regularLID := types.JID{User: businessLID.User, Device: HostedDeviceID, Server: types.HiddenUserServer}
	hosted := newSignalTestPeer(t, companion, hostedLID)
	regular := newSignalTestPeer(t, companion, regularLID)

	encType, content := hosted.encrypt(t, "hosted 1")
	if encType != "pkmsg" {
		t.Fatalf("first hosted message type = %s, want pkmsg", encType)
	}
	info := deliverSignalTestMessage(t, cli, hostedLID, "3EB0H1", encType, content)
	// The public sender keeps the existing normalization.
	if info.Sender != regularLID {
		t.Fatalf("MessageInfo.Sender = %s, want %s", info.Sender, regularLID)
	}
	encType, content = regular.encrypt(t, "regular 1")
	deliverSignalTestMessage(t, cli, regularLID, "3EB0R1", encType, content)

	// The companion replies through the send fanout path, addressed to the PN
	// hosted device from a device list. It must reuse the hosted session.
	hostedPN := types.JID{User: business.User, Device: HostedDeviceID, Server: types.HostedServer}
	reply, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("reply")})
	if err != nil {
		t.Fatal(err)
	}
	participants, _, err := cli.encryptMessageForDevices(context.Background(), []types.JID{hostedPN}, "3EB0REPLY", reply, nil, waBinary.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(participants) != 1 || participants[0].AttrGetter().JID("jid") != hostedPN {
		t.Fatalf("reply participants = %v", participants)
	}
	enc := participants[0].GetChildByTag("enc")
	if enc.AttrGetter().String("type") != "msg" {
		t.Fatalf("reply enc type = %v, want msg on the existing hosted session", enc.Attrs["type"])
	}
	replyMsg, err := protocol.NewSignalMessageFromBytes(enc.Content.([]byte), pbSerializer.SignalMessage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hosted.cipher.Decrypt(context.Background(), replyMsg); err != nil {
		t.Fatalf("hosted peer could not decrypt the reply: %v", err)
	}

	// After the reply, the hosted peer sends a normal msg.
	encType, content = hosted.encrypt(t, "hosted 2")
	if encType != "msg" {
		t.Fatalf("second hosted message type = %s, want msg", encType)
	}
	deliverSignalTestMessage(t, cli, hostedLID, "3EB0H2", encType, content)
	encType, content = regular.encrypt(t, "regular 2")
	deliverSignalTestMessage(t, cli, regularLID, "3EB0R2", encType, content)

	if undecryptable != 0 {
		t.Fatalf("got %d undecryptable messages", undecryptable)
	}
	if want := []string{"hosted 1", "regular 1", "hosted 2", "regular 2"}; !slices.Equal(got, want) {
		t.Fatalf("decrypted = %q, want %q", got, want)
	}
	wantAddrs := []string{hostedLID.SignalAddress().String(), regularLID.SignalAddress().String()}
	slices.Sort(wantAddrs)
	if addrs := mem.sessionAddresses(); !slices.Equal(addrs, wantAddrs) {
		t.Fatalf("session addresses = %v, want %v", addrs, wantAddrs)
	}
	if wantAddrs[0] == wantAddrs[1] {
		t.Fatal("hosted and regular device 99 share a Signal address")
	}

	// A retry receipt from the hosted device is answered on the hosted session
	// and addressed to the raw hosted JID.
	retried, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("retried")})
	if err != nil {
		t.Fatal(err)
	}
	var retriedMsg waE2E.Message
	if err = proto.Unmarshal(retried, &retriedMsg); err != nil {
		t.Fatal(err)
	}
	retryNode := &waBinary.Node{
		Tag:   "receipt",
		Attrs: waBinary.Attrs{"from": hostedLID, "id": "3EB0OUT", "t": "1700000200", "type": "retry"},
		Content: []waBinary.Node{
			{Tag: "retry", Attrs: waBinary.Attrs{"count": "1", "id": "3EB0OUT", "t": "1700000100", "v": "1"}},
		},
	}
	receipt, _, err := cli.parseReceipt(retryNode)
	if err != nil {
		t.Fatal(err)
	}
	if err = cli.addRecentMessage(context.Background(), receipt.Chat, "3EB0OUT", &retriedMsg, nil); err != nil {
		t.Fatal(err)
	}
	before := len(sent.nodes)
	if err = cli.handleRetryReceipt(context.Background(), receipt, retryNode); err != nil {
		t.Fatal(err)
	}
	var resent *waBinary.Node
	for _, node := range sent.nodes[before:] {
		if node.Tag == "message" {
			resent = node
		}
	}
	if resent == nil || resent.AttrGetter().JID("to") != hostedLID {
		t.Fatalf("retry message = %v, want one addressed to %s", resent, hostedLID)
	}
	retryEnc := resent.GetChildByTag("enc")
	if retryEnc.AttrGetter().String("type") != "msg" {
		t.Fatalf("retry enc type = %v, want msg on the hosted session", retryEnc.Attrs["type"])
	}
	retryMsg, err := protocol.NewSignalMessageFromBytes(retryEnc.Content.([]byte), pbSerializer.SignalMessage)
	if err != nil {
		t.Fatal(err)
	}
	padded, err := hosted.cipher.Decrypt(context.Background(), retryMsg)
	if err != nil {
		t.Fatalf("hosted peer could not decrypt the retry: %v", err)
	}
	plain, err := unpadMessage(padded, 2)
	if err != nil {
		t.Fatal(err)
	}
	var got2 waE2E.Message
	if err = proto.Unmarshal(plain, &got2); err != nil || got2.GetConversation() != "retried" {
		t.Fatalf("retry plaintext = %v, %v", got2.GetConversation(), err)
	}
}

func TestHostedSignalJIDMatchesWebAddressing(t *testing.T) {
	business := types.NewJID("15551234567", types.DefaultUserServer)
	businessLID := types.NewJID("123456789012345", types.HiddenUserServer)
	hostedPN := types.JID{User: business.User, Device: HostedDeviceID, Server: types.HostedServer}
	hostedLID := types.JID{User: businessLID.User, Device: HostedDeviceID, Server: types.HostedLIDServer}
	ownPN := types.JID{User: "15550000000", Device: 7, Server: types.DefaultUserServer}

	withLID, _ := newSignalTestDevice(ownPN, &lidStub{pn: business, lid: businessLID})
	cli := NewClient(withLID, waLog.Noop)
	// WAWebSignalAddress:14-33: <lid>:99@hosted.lid when the LID is known.
	if got := cli.hostedSignalJID(context.Background(), hostedPN); got != hostedLID {
		t.Fatalf("hosted PN with known LID = %s, want %s", got, hostedLID)
	}
	if got := cli.hostedSignalJID(context.Background(), hostedLID); got != hostedLID {
		t.Fatalf("hosted LID = %s, want %s", got, hostedLID)
	}
	withoutLID, _ := newSignalTestDevice(ownPN, &store.NoopStore{})
	cli = NewClient(withoutLID, waLog.Noop)
	// ...and <pn>:99@hosted otherwise.
	if got := cli.hostedSignalJID(context.Background(), hostedPN); got != hostedPN {
		t.Fatalf("hosted PN without LID = %s, want %s", got, hostedPN)
	}
	for _, regular := range []types.JID{
		{User: businessLID.User, Device: HostedDeviceID, Server: types.HiddenUserServer},
		{User: business.User, Device: HostedDeviceID, Server: types.DefaultUserServer},
	} {
		for _, hosted := range []types.JID{hostedPN, hostedLID} {
			if regular.SignalAddress().String() == hosted.SignalAddress().String() {
				t.Fatalf("%s and %s share Signal address %s", regular, hosted, regular.SignalAddress())
			}
		}
	}
}

func TestRawHostedSender(t *testing.T) {
	hosted := types.JID{User: "123456789012345", Device: HostedDeviceID, Server: types.HostedLIDServer}
	for _, tc := range []struct {
		attrs waBinary.Attrs
		want  types.JID
		ok    bool
	}{
		{waBinary.Attrs{"from": hosted}, hosted, true},
		{waBinary.Attrs{"from": types.NewJID("1203630000", types.GroupServer), "participant": hosted}, hosted, true},
		{waBinary.Attrs{"from": types.JID{User: hosted.User, Device: 99, Server: types.HiddenUserServer}}, types.EmptyJID, false},
	} {
		got, ok := rawHostedSender(&waBinary.Node{Tag: "message", Attrs: tc.attrs})
		if got != tc.want || ok != tc.ok {
			t.Fatalf("rawHostedSender(%v) = %s, %v; want %s, %v", tc.attrs, got, ok, tc.want, tc.ok)
		}
	}
}

func TestHostedLIDTransitionMigratesEstablishedPNSession(t *testing.T) {
	ctx := context.Background()
	own := types.JID{User: "15550000000", Device: 7, Server: types.DefaultUserServer}
	pn := types.JID{User: "15551234567", Device: HostedDeviceID, Server: types.HostedServer}
	lid := types.JID{User: "123456789012345", Device: HostedDeviceID, Server: types.HostedLIDServer}
	aliases := &lidStub{}
	companion, mem := newSignalTestDevice(own, aliases)
	cli := NewClient(companion, waLog.Noop)
	cli.SynchronousAck = true
	cli.testSendHook = (&sentNodes{}).hook
	var received []string
	cli.AddEventHandler(func(evt any) {
		if event, ok := evt.(*events.Message); ok {
			received = append(received, event.Message.GetConversation())
		}
	})
	remote := newSignalTestPeer(t, companion, pn)
	kind, ciphertext := remote.encrypt(t, "before alias")
	deliverSignalTestMessage(t, cli, pn, "3EB0PN", kind, ciphertext)
	// A reply acknowledges the initial prekey session so the next inbound
	// ciphertext needs that established session, rather than a new prekey.
	reply, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("ack")})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _, err := cli.encryptMessageForDevices(ctx, []types.JID{pn}, "3EB0ACK", reply, nil, waBinary.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	enc := nodes[0].GetChildByTag("enc")
	signal, err := protocol.NewSignalMessageFromBytes(enc.Content.([]byte), pbSerializer.SignalMessage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.cipher.Decrypt(ctx, signal); err != nil {
		t.Fatal(err)
	}
	regular := types.JID{User: pn.User, Device: HostedDeviceID, Server: types.DefaultUserServer}.SignalAddress().String()
	mem.sessions[regular] = []byte("regular session stays separate")
	aliases.pn = types.NewJID(pn.User, types.DefaultUserServer)
	aliases.lid = types.NewJID(lid.User, types.HiddenUserServer)
	kind, ciphertext = remote.encrypt(t, "after alias")
	if kind != "msg" {
		t.Fatalf("transition ciphertext type = %s, want msg", kind)
	}
	deliverSignalTestMessage(t, cli, lid, "3EB0LID", kind, ciphertext)
	if !slices.Equal(received, []string{"before alias", "after alias"}) {
		t.Fatalf("decrypted = %q", received)
	}
	if found, _ := mem.HasSession(ctx, pn.SignalAddress().String()); found {
		t.Fatal("old hosted PN session remains")
	}
	if found, _ := mem.HasSession(ctx, lid.SignalAddress().String()); !found {
		t.Fatal("hosted LID session is missing")
	}
	if string(mem.sessions[regular]) != "regular session stays separate" {
		t.Fatal("regular device99 session changed")
	}
}
