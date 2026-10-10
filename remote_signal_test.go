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

func TestRemotePreKeyStoreMarksWholeBatches(t *testing.T) {
	_, remote, _ := newTestRemote(t)
	pk := &remotePreKeyStore{remote: remote}
	ctx := context.Background()
	first, _ := pk.GetOrGenPreKeys(ctx, 3)  // IDs 1..3
	second, _ := pk.GetOrGenPreKeys(ctx, 2) // IDs 4..5, generated before the first is marked
	if err := pk.MarkPreKeysAsUploaded(ctx, first[len(first)-1].KeyID); err != nil {
		t.Fatal(err)
	}
	if err := pk.MarkPreKeysAsUploaded(ctx, second[len(second)-1].KeyID); err != nil {
		t.Fatal(err)
	}
	want := [][]uint32{{1, 2, 3}, {4, 5}}
	if len(remote.marked) != 2 || !equalIDs(remote.marked[0], want[0]) || !equalIDs(remote.marked[1], want[1]) {
		t.Fatalf("marked %v, want %v", remote.marked, want)
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

func TestRemoteDeviceSignatureChecksTheSignedDetails(t *testing.T) {
	cli, remote, _ := newTestRemote(t)
	verified := &waAdv.ADVSignedDeviceIdentity{Details: []byte("details")}
	sign := func(details []byte, sigLen int) []byte {
		b, _ := proto.Marshal(&waAdv.ADVSignedDeviceIdentity{Details: details, DeviceSignature: make([]byte, sigLen)})
		return b
	}
	remote.signed = sign([]byte("other details"), 64)
	if _, err := cli.remoteDeviceSignature(context.Background(), nil, verified); err == nil {
		t.Fatal("a signature over different details was accepted")
	}
	remote.signed = sign([]byte("details"), 10)
	if _, err := cli.remoteDeviceSignature(context.Background(), nil, verified); err == nil {
		t.Fatal("a short signature was accepted")
	}
	remote.signed = sign([]byte("details"), 64)
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
