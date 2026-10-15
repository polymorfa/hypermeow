// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waAdv"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	"github.com/polymorfa/hypermeow/util/keys"
)

type fakeRemoteSignal struct {
	mu sync.Mutex

	receives   []RemoteReceive
	receiveErr error
	result     RemoteReceiveResult
	nextID     uint32
	marked     [][]uint32
	signed     []byte
	ownJIDs    []types.JID
	resends    []RemoteResend
	resendWith []byte
	forgotten  []types.JID
	migrated   [][2]types.JID
	migrateErr error
}

func (f *fakeRemoteSignal) MigratePNToLID(ctx context.Context, pn, lid types.JID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.migrated = append(f.migrated, [2]types.JID{pn, lid})
	return f.migrateErr
}

func (f *fakeRemoteSignal) Receive(ctx context.Context, req RemoteReceive) (*RemoteReceiveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receives = append(f.receives, req)
	if f.receiveErr != nil {
		return nil, f.receiveErr
	}
	result := f.result
	return &result, nil
}

func (f *fakeRemoteSignal) GeneratePreKeys(ctx context.Context, count uint32) ([]*keys.PreKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*keys.PreKey, 0, count)
	for i := uint32(0); i < count; i++ {
		f.nextID++
		pub := [32]byte{byte(f.nextID)}
		out = append(out, &keys.PreKey{KeyPair: keys.KeyPair{Pub: &pub}, KeyID: f.nextID})
	}
	return out, nil
}

func (f *fakeRemoteSignal) MarkPreKeysUploaded(ctx context.Context, ids []uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marked = append(f.marked, append([]uint32(nil), ids...))
	return nil
}

func (f *fakeRemoteSignal) UploadedPreKeyCount(ctx context.Context) (int, error) { return 7, nil }

func (f *fakeRemoteSignal) SignPairing(ctx context.Context, container []byte) ([]byte, error) {
	return f.signed, nil
}

func (f *fakeRemoteSignal) SetOwnJIDs(ctx context.Context, pn, lid types.JID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ownJIDs = append(f.ownJIDs, pn, lid)
	return nil
}

func (f *fakeRemoteSignal) ForgetIdentity(ctx context.Context, user types.JID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotten = append(f.forgotten, user)
	return nil
}

func (f *fakeRemoteSignal) Resend(ctx context.Context, req RemoteResend) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resends = append(f.resends, req)
	return f.resendWith, nil
}

var _ RemoteSignal = (*fakeRemoteSignal)(nil)

type sentNodes struct {
	mu    sync.Mutex
	nodes []*waBinary.Node
}

func (s *sentNodes) hook(payload []byte) error {
	unpacked, err := waBinary.Unpack(payload)
	if err != nil {
		return err
	}
	node, err := waBinary.Unmarshal(unpacked)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.nodes = append(s.nodes, node)
	s.mu.Unlock()
	return nil
}

func (s *sentNodes) tags() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, n := range s.nodes {
		out = append(out, n.Tag+":"+n.AttrGetter().OptionalString("type"))
	}
	return out
}

var testRemoteIdentity = RemoteIdentity{
	RegistrationID:        4242,
	IdentityKey:           [32]byte{1},
	SignedPreKeyID:        3,
	SignedPreKey:          [32]byte{2},
	SignedPreKeySignature: [64]byte{3},
	AdvSecret:             [32]byte{4},
}

func newTestRemote(t *testing.T) (*Client, *fakeRemoteSignal, *sentNodes) {
	t.Helper()
	remote := &fakeRemoteSignal{}
	cli := NewRemoteSignalClient(&store.Device{}, testRemoteIdentity, remote, nil)
	sent := &sentNodes{}
	cli.testSendHook = sent.hook
	cli.SynchronousAck = true
	return cli, remote, sent
}

func TestRemoteSignalClientHoldsOnlyPublicKeys(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	dev := cli.Store
	if !dev.RemoteKeys {
		t.Fatal("device is not marked remote-keyed")
	}
	if *dev.IdentityKey.Pub != testRemoteIdentity.IdentityKey || *dev.IdentityKey.Priv != [32]byte{} {
		t.Fatal("identity key should be public-only")
	}
	if *dev.SignedPreKey.Priv != [32]byte{} || *dev.SignedPreKey.Signature != testRemoteIdentity.SignedPreKeySignature {
		t.Fatal("signed prekey should carry the remote signature and no private key")
	}
	if dev.RegistrationID != 4242 || string(dev.AdvSecretKey) != string(testRemoteIdentity.AdvSecret[:]) {
		t.Fatal("registration ID or ADV secret not applied")
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "remote keys") {
			t.Fatalf("local identity read on a remote-keyed device did not panic: %v", r)
		}
	}()
	dev.GetIdentityKeyPair()
}

func TestNewRemoteSignalClientRejectsNilRemote(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for a nil RemoteSignal")
		}
	}()
	NewRemoteSignalClient(&store.Device{}, testRemoteIdentity, nil, nil)
}

func encMessage(encType string) (*types.MessageInfo, *waBinary.Node) {
	sender := types.NewJID("123", types.HiddenUserServer)
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: sender, Sender: sender},
		ID:            "MSG1",
	}
	node := &waBinary.Node{
		Tag:   "message",
		Attrs: waBinary.Attrs{"id": "MSG1", "from": sender, "t": "1700000000"},
		Content: []waBinary.Node{{
			Tag:     "enc",
			Attrs:   waBinary.Attrs{"type": encType, "v": "2"},
			Content: []byte("ciphertext"),
		}},
	}
	return info, node
}

func TestRemoteReceiveDeliversMetadataAndAcknowledges(t *testing.T) {
	cli, remote, sent := newTestRemote(t)
	remote.result = RemoteReceiveResult{Redelivered: true}
	var got []*events.RemoteMessage
	cli.AddEventHandler(func(evt any) {
		if e, ok := evt.(*events.RemoteMessage); ok {
			got = append(got, e)
		}
	})
	info, node := encMessage("pkmsg")
	cli.decryptMessages(context.Background(), info, node)

	if len(remote.receives) != 1 {
		t.Fatalf("Receive calls = %d, want 1", len(remote.receives))
	}
	req := remote.receives[0]
	if req.Kind != "pkmsg" || string(req.Ciphertext) != "ciphertext" || req.PaddingVersion != 2 || req.Sender != info.Sender {
		t.Fatalf("unexpected receive request: %+v", req)
	}
	if len(got) != 1 || !got[0].Redelivered || got[0].Info.ID != "MSG1" {
		t.Fatalf("RemoteMessage events = %+v", got)
	}
	if tags := sent.tags(); len(tags) != 1 || !strings.HasPrefix(tags[0], "receipt:") {
		t.Fatalf("sent %v, want one delivery receipt", tags)
	}
}

func TestRemoteReceivePreservesProviderTimeAndAlternateSender(t *testing.T) {
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789", types.HiddenUserServer)
	stamp := time.Unix(1720000000, 123456789).UTC()
	for _, tc := range []struct {
		name                string
		sender, alternate   types.JID
		wantSender, wantAlt types.JID
		timestamp           time.Time
	}{
		{"lid addressed", lid, pn, lid, pn, stamp},
		{"pn normalized to lid", pn, lid, lid, pn, stamp},
		{"absent metadata", lid, types.JID{}, lid, types.JID{}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli, remote, sent := newTestRemote(t)
			info, node := encMessage("pkmsg")
			info.Sender, info.SenderAlt = tc.sender, tc.alternate
			info.Chat = tc.sender.ToNonAD()
			info.Timestamp = tc.timestamp
			cli.decryptMessages(context.Background(), info, node)
			if len(remote.receives) != 1 {
				t.Fatalf("Receive calls = %d, want 1", len(remote.receives))
			}
			req := remote.receives[0]
			if !req.Timestamp.Equal(tc.timestamp) || req.Sender != tc.wantSender || req.SenderAlt != tc.wantAlt {
				t.Fatalf("receive metadata = (%v, %s, %s), want (%v, %s, %s)", req.Timestamp, req.Sender, req.SenderAlt, tc.timestamp, tc.wantSender, tc.wantAlt)
			}
			if req.Kind != "pkmsg" || req.MessageID != info.ID || req.Chat != info.Chat || string(req.Ciphertext) != "ciphertext" {
				t.Fatal("adding receive metadata changed the encrypted payload or routing")
			}
			if tags := sent.tags(); len(tags) != 1 || !strings.HasPrefix(tags[0], "receipt:") {
				t.Fatalf("sent %v, want the unchanged delivery receipt", tags)
			}
		})
	}
}

func TestRemoteAlreadyDeliveredOnlyAcknowledges(t *testing.T) {
	cli, remote, sent := newTestRemote(t)
	remote.result = RemoteReceiveResult{AlreadyDelivered: true}
	events_ := 0
	cli.AddEventHandler(func(evt any) {
		if _, ok := evt.(*events.RemoteMessage); ok {
			events_++
		}
	})
	info, node := encMessage("msg")
	cli.decryptMessages(context.Background(), info, node)
	if events_ != 0 {
		t.Fatal("an already-delivered payload must not be announced again")
	}
	if tags := sent.tags(); len(tags) != 1 || !strings.HasPrefix(tags[0], "receipt:") {
		t.Fatalf("sent %v, want the receipt again", tags)
	}
}

func TestRemoteDecryptFailureSendsRetryReceipt(t *testing.T) {
	cli, remote, sent := newTestRemote(t)
	remote.receiveErr = &RemoteDecryptError{Failure: RemoteDecryptOther, Err: errors.New("bad mac")}
	var undecryptable int
	cli.AddEventHandler(func(evt any) {
		if _, ok := evt.(*events.UndecryptableMessage); ok {
			undecryptable++
		}
	})
	info, node := encMessage("msg")
	cli.decryptMessages(context.Background(), info, node)
	tags := sent.tags()
	if len(tags) < 1 || tags[0] != "receipt:retry" {
		t.Fatalf("sent %v, want a retry receipt first", tags)
	}
	if undecryptable != 1 {
		t.Fatalf("UndecryptableMessage events = %d, want 1", undecryptable)
	}
}

func TestRemoteUndecodableMessageIsNacked(t *testing.T) {
	cli, remote, sent := newTestRemote(t)
	remote.receiveErr = &RemoteDecryptError{Failure: RemoteDecryptUndecodable, Err: errors.New("bad proto")}
	info, node := encMessage("msg")
	cli.decryptMessages(context.Background(), info, node)
	tags := sent.tags()
	if len(tags) != 1 || !strings.HasPrefix(tags[0], "ack:") {
		t.Fatalf("sent %v, want a nack", tags)
	}
	if code := sent.nodes[0].AttrGetter().OptionalInt("error"); code != NackInvalidProtobuf {
		t.Fatalf("nack code = %d, want %d", code, NackInvalidProtobuf)
	}
}

func TestRemotePreKeyStoreReusesUnuploadedKeysAndMarksOnlyTheUploadedBatch(t *testing.T) {
	_, remote, _ := newTestRemote(t)
	pk := &remotePreKeyStore{remote: remote}
	ctx := context.Background()
	ids := func(batch []*keys.PreKey) []uint32 {
		var out []uint32
		for _, k := range batch {
			out = append(out, k.KeyID)
		}
		return out
	}
	first, _ := pk.GetOrGenPreKeys(ctx, 3) // 1..3; the upload then fails, nothing is marked
	again, _ := pk.GetOrGenPreKeys(ctx, 3)
	if !equalIDs(ids(again), ids(first)) || remote.nextID != 3 {
		t.Fatalf("retry after a failed upload got %v (generated up to %d), want the same keys", ids(again), remote.nextID)
	}
	partial, _ := pk.GetOrGenPreKeys(ctx, 2) // 1..2 of the pending three
	if err := pk.MarkPreKeysAsUploaded(ctx, partial[len(partial)-1].KeyID); err != nil {
		t.Fatal(err)
	}
	if len(remote.marked) != 1 || !equalIDs(remote.marked[0], []uint32{1, 2}) {
		t.Fatalf("marked %v, want only the uploaded batch [1 2]", remote.marked)
	}
	next, _ := pk.GetOrGenPreKeys(ctx, 2) // 3 is still pending; one new key
	if !equalIDs(ids(next), []uint32{3, 4}) {
		t.Fatalf("next batch %v, want [3 4]", ids(next))
	}
	retry, err := pk.GenOnePreKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last := remote.marked[len(remote.marked)-1]
	if !equalIDs(last, []uint32{retry.KeyID}) {
		t.Fatalf("retry-receipt prekey %d was not marked published (last mark %v)", retry.KeyID, last)
	}
	if n, _ := pk.UploadedPreKeyCount(ctx); n != 7 {
		t.Fatalf("UploadedPreKeyCount = %d", n)
	}
}

func equalIDs(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRemoteDeviceSignatureMustVerify(t *testing.T) {
	identity := keys.NewKeyPair()
	remoteIdentity := testRemoteIdentity
	remoteIdentity.IdentityKey = *identity.Pub
	remote := &fakeRemoteSignal{}
	cli := NewRemoteSignalClient(&store.Device{}, remoteIdentity, remote, nil)
	account := keys.NewKeyPair()
	verified := &waAdv.ADVSignedDeviceIdentity{Details: []byte("details"), AccountSignatureKey: account.Pub[:]}
	signedBy := func(details []byte, signature []byte) []byte {
		b, _ := proto.Marshal(&waAdv.ADVSignedDeviceIdentity{Details: details, DeviceSignature: signature})
		return b
	}
	valid := generateDeviceSignature(verified, identity)[:]

	remote.signed = signedBy([]byte("other details"), valid)
	if _, err := cli.remoteDeviceSignature(context.Background(), nil, verified); err == nil {
		t.Fatal("a signature over different details was accepted")
	}
	remote.signed = signedBy([]byte("details"), make([]byte, 64))
	if _, err := cli.remoteDeviceSignature(context.Background(), nil, verified); err == nil {
		t.Fatal("a 64-byte signature that does not verify was accepted")
	}
	other := generateDeviceSignature(verified, keys.NewKeyPair())[:]
	remote.signed = signedBy([]byte("details"), other)
	if _, err := cli.remoteDeviceSignature(context.Background(), nil, verified); err == nil {
		t.Fatal("a signature by a different identity was accepted")
	}
	remote.signed = signedBy([]byte("details"), valid)
	if sig, err := cli.remoteDeviceSignature(context.Background(), nil, verified); err != nil || len(sig) != 64 {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func marshalStanza(t *testing.T, node waBinary.Node) []byte {
	t.Helper()
	b, err := waBinary.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSendPrebuiltStanzaWaitsForTheAck(t *testing.T) {
	cli, _, sent := newTestRemote(t)
	stanza := marshalStanza(t, waBinary.Node{
		Tag:   "message",
		Attrs: waBinary.Attrs{"id": "OUT1", "to": types.NewJID("123", types.HiddenUserServer), "type": "text"},
	})
	done := make(chan struct{})
	var resp SendResponse
	var err error
	go func() {
		resp, err = cli.SendPrebuiltStanza(context.Background(), stanza, "", 5*time.Second)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(sent.tags()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := cli.InjectNode(context.Background(), &waBinary.Node{
		Tag:   "ack",
		Attrs: waBinary.Attrs{"id": "OUT1", "class": "message", "t": "1700000001"},
	}); err != nil {
		t.Fatal(err)
	}
	<-done
	if err != nil || resp.ID != "OUT1" || resp.Timestamp.Unix() != 1700000001 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if tags := sent.tags(); len(tags) != 1 || tags[0] != "message:text" {
		t.Fatalf("sent %v", tags)
	}
}

func TestSendPrebuiltStanzaRejectsNonMessages(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	stanza := marshalStanza(t, waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{"id": "X"}})
	if _, err := cli.SendPrebuiltStanza(context.Background(), stanza, "", time.Second); err == nil {
		t.Fatal("a non-message stanza was sent")
	}
}

func TestRemoteRetryReceiptAsksTheStoreToResend(t *testing.T) {
	cli, remote, sent := newTestRemote(t)
	requester := types.JID{User: "123", Server: types.HiddenUserServer, Device: 2}
	remote.resendWith = marshalStanza(t, waBinary.Node{
		Tag:   "message",
		Attrs: waBinary.Attrs{"id": "MSG9", "to": requester, "type": "text"},
	})
	receipt := &events.Receipt{
		MessageSource: types.MessageSource{Chat: requester.ToNonAD(), Sender: requester},
	}
	node := &waBinary.Node{
		Tag:   "receipt",
		Attrs: waBinary.Attrs{"id": "MSG9", "from": requester, "type": "retry"},
		Content: []waBinary.Node{{
			Tag:   "retry",
			Attrs: waBinary.Attrs{"id": "MSG9", "t": "1700000000", "count": "1"},
		}},
	}
	if err := cli.handleRetryReceipt(context.Background(), receipt, node); err != nil {
		t.Fatal(err)
	}
	if len(remote.resends) != 1 || remote.resends[0].MessageID != "MSG9" || remote.resends[0].RetryCount != 1 {
		t.Fatalf("resends = %+v", remote.resends)
	}
	if tags := sent.tags(); len(tags) != 1 || tags[0] != "message:text" {
		t.Fatalf("sent %v", tags)
	}
}

func TestRemoteClientRefusesLocalSends(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	_, err := cli.SendMessage(context.Background(), types.NewJID("123", types.DefaultUserServer), nil)
	if !errors.Is(err, ErrRemoteSignalUnsupported) {
		t.Fatalf("SendMessage err = %v", err)
	}
	if _, err := cli.PairPhone(context.Background(), "15550001111", false, PairClientChrome, "Chrome (Linux)"); !errors.Is(err, ErrRemoteSignalUnsupported) {
		t.Fatalf("PairPhone err = %v", err)
	}
}

func TestRemoteStoreOutageNeitherAcksNorRetries(t *testing.T) {
	cli, remote, sent := newTestRemote(t)
	remote.receiveErr = errors.New("connection refused")
	announced := 0
	cli.AddEventHandler(func(evt any) {
		switch evt.(type) {
		case *events.RemoteMessage, *events.UndecryptableMessage:
			announced++
		}
	})
	info, node := encMessage("msg")
	cli.decryptMessages(context.Background(), info, node)
	if tags := sent.tags(); len(tags) != 0 {
		t.Fatalf("sent %v during a store outage; the server must redeliver", tags)
	}
	if announced != 0 {
		t.Fatal("no event should be emitted for a payload the store never processed")
	}
}

func TestRemoteRetryHonoursPreRetryCallback(t *testing.T) {
	cli, remote, sent := newTestRemote(t)
	var gotMessage bool
	cli.PreRetryCallback = func(_ *events.Receipt, id types.MessageID, _ int, msg *waE2E.Message) bool {
		gotMessage = msg != nil
		return false
	}
	requester := types.JID{User: "123", Server: types.HiddenUserServer, Device: 2}
	receipt := &events.Receipt{MessageSource: types.MessageSource{Chat: requester.ToNonAD(), Sender: requester}}
	node := &waBinary.Node{
		Tag:   "receipt",
		Attrs: waBinary.Attrs{"id": "MSG8", "from": requester, "type": "retry"},
		Content: []waBinary.Node{{
			Tag:   "retry",
			Attrs: waBinary.Attrs{"id": "MSG8", "t": "1700000000", "count": "1"},
		}},
	}
	if err := cli.handleRetryReceipt(context.Background(), receipt, node); err != nil {
		t.Fatal(err)
	}
	if len(remote.resends) != 0 || len(sent.tags()) != 0 || gotMessage {
		t.Fatalf("rejected retry still resent (resends=%d, sent=%v, message=%v)", len(remote.resends), sent.tags(), gotMessage)
	}
}

func TestRemoteIdentityChangeReachesTheStore(t *testing.T) {
	for _, tc := range []struct{ from, want types.JID }{
		{types.JID{User: "123", Server: types.HiddenUserServer, Device: 3}, types.NewJID("123", types.HiddenUserServer)},
		{types.JID{User: "123", Server: types.HostedLIDServer, Device: 99}, types.NewJID("123", types.HiddenUserServer)},
		{types.JID{User: "5511", Server: types.HostedServer, Device: 99}, types.NewJID("5511", types.DefaultUserServer)},
	} {
		cli, remote, _ := newTestRemote(t)
		cli.deleteIdentityChangeState(context.Background(), tc.from)
		if len(remote.forgotten) != 1 || remote.forgotten[0] != tc.want {
			t.Fatalf("from %s: forgotten = %v, want [%s]", tc.from, remote.forgotten, tc.want)
		}
	}
}

func TestRemoteClientRefusesLocalFBSends(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	_, err := cli.SendFBMessage(context.Background(), types.NewJID("123", types.DefaultUserServer), nil, nil)
	if !errors.Is(err, ErrRemoteSignalUnsupported) {
		t.Fatalf("SendFBMessage err = %v", err)
	}
}

func TestRemotePNSenderMigratesSessionsInTheRemoteStore(t *testing.T) {
	cli, remote, _ := newTestRemote(t)
	pn := types.JID{User: "5511", Server: types.DefaultUserServer, Device: 3}
	lid := types.JID{User: "777", Server: types.HiddenUserServer, Device: 3}
	receive := func() {
		info, node := encMessage("msg")
		info.Chat, info.Sender, info.SenderAlt = pn.ToNonAD(), pn, lid
		node.Attrs["from"] = pn
		cli.decryptMessages(context.Background(), info, node)
	}

	remote.migrateErr = errors.New("store offline")
	receive()
	remote.migrateErr = nil
	receive()
	receive()

	want := [2]types.JID{pn.ToNonAD(), lid.ToNonAD()}
	if len(remote.migrated) != 2 || remote.migrated[0] != want || remote.migrated[1] != want {
		t.Fatalf("migrations = %v, want a failed then one successful %v and no more", remote.migrated, want)
	}
	if len(remote.receives) != 3 || remote.receives[2].Sender != lid {
		t.Fatalf("receives should decrypt as the LID %s: %+v", lid, remote.receives)
	}
}
