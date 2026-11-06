// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/util/keys"
)

func preKeyIDs(batch []*keys.PreKey) []uint32 {
	ids := make([]uint32, len(batch))
	for i, key := range batch {
		ids[i] = key.KeyID
	}
	return ids
}

func publicTestPreKey(id uint32) *keys.PreKey {
	pub := [32]byte{1}
	return &keys.PreKey{KeyPair: keys.KeyPair{Pub: &pub}, KeyID: id}
}

func TestRemotePreKeyUploadWrapAndConsumedMembers(t *testing.T) {
	for _, consumeBeforePrepare := range []bool{false, true} {
		t.Run(fmt.Sprint(consumeBeforePrepare), func(t *testing.T) {
			remote := &fakeRemoteSignal{batch: &RemotePreKeyUploadBatch{
				BatchID: strings.Repeat("a", 32),
				PreKeys: []*keys.PreKey{publicTestPreKey(0xFFFFFE), publicTestPreKey(0xFFFFFF), publicTestPreKey(2)},
			}}
			pk := &remotePreKeyStore{remote: remote}
			if consumeBeforePrepare {
				remote.batch.PreKeys = remote.batch.PreKeys[2:]
			}
			batch, err := pk.GetOrGenPreKeys(context.Background(), 50)
			if err != nil {
				t.Fatal(err)
			}
			want := []uint32{0xFFFFFE, 0xFFFFFF, 2}
			if consumeBeforePrepare {
				want = []uint32{2}
			} else {
				// These original members are consumed after preparation, but ACK
				// must retain the complete immutable upload snapshot.
				remote.batch.PreKeys = remote.batch.PreKeys[2:]
			}
			if !equalIDs(preKeyIDs(batch), want) || remote.nextID != 0 {
				t.Fatalf("keys=%v allocated=%d", preKeyIDs(batch), remote.nextID)
			}
			if err := pk.MarkPreKeysAsUploaded(context.Background(), 0xFFFFFF); err == nil || len(remote.acks) != 0 {
				t.Fatal("nonlast numeric maximum acknowledged a wrapped batch")
			}
			if err := pk.MarkPreKeysAsUploaded(context.Background(), 2); err != nil {
				t.Fatal(err)
			}
			if len(remote.acks) != 1 || !equalIDs(remote.acks[0].ids, want) || remote.batch != nil || len(remote.marked) != 0 {
				t.Fatalf("acks=%v legacy marks=%v", remote.acks, remote.marked)
			}
			if err := pk.MarkPreKeysAsUploaded(context.Background(), 2); err == nil || len(remote.acks) != 1 {
				t.Fatal("completed snapshot remained usable")
			}
		})
	}
}

type boundaryPreKeyRemote struct {
	*fakeRemoteSignal
	prepared   *RemotePreKeyUploadBatch
	prepareErr error
	fresh      []*keys.PreKey
	ack        func(string, []uint32) error
}

func (r *boundaryPreKeyRemote) PreparePreKeyUpload(context.Context, uint32) (*RemotePreKeyUploadBatch, error) {
	return r.prepared, r.prepareErr
}

func (r *boundaryPreKeyRemote) GeneratePreKeys(context.Context, uint32) ([]*keys.PreKey, error) {
	return r.fresh, nil
}

func (r *boundaryPreKeyRemote) AcknowledgePreKeyUpload(_ context.Context, token string, ids []uint32) error {
	return r.ack(token, ids)
}

func TestRemotePreKeyUploadDefensiveSnapshotsAndACKRetry(t *testing.T) {
	input := []*keys.PreKey{publicTestPreKey(10), publicTestPreKey(11)}
	var calls []testPreKeyACK
	remote := &boundaryPreKeyRemote{fakeRemoteSignal: &fakeRemoteSignal{}, prepared: &RemotePreKeyUploadBatch{BatchID: strings.Repeat("b", 32), PreKeys: input}}
	remote.ack = func(token string, ids []uint32) error {
		calls = append(calls, testPreKeyACK{token, append([]uint32(nil), ids...)})
		ids[0] = 999 // A callback must not corrupt the next retry's authority.
		if len(calls) == 1 {
			return errors.New("synthetic response loss")
		}
		return nil
	}
	pk := &remotePreKeyStore{remote: remote}
	batch, err := pk.GetOrGenPreKeys(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Pub[0] = 42
	input[0].KeyID = 30
	if batch[0].Pub[0] != 1 || batch[0].KeyID != 10 {
		t.Fatal("remote response pointers escaped without a copy")
	}
	batch[0].KeyID, batch[1].KeyID = 100, 101
	batch[0].Pub[0] = 99
	if input[0].Pub[0] != 42 {
		t.Fatal("caller mutated remote public key storage")
	}
	if err := pk.MarkPreKeysAsUploaded(context.Background(), 101); err == nil || len(calls) != 0 {
		t.Fatal("caller-mutated last ID became ACK authority")
	}
	if err := pk.MarkPreKeysAsUploaded(context.Background(), 11); err == nil {
		t.Fatal("synthetic first ACK failure was hidden")
	}
	if err := pk.MarkPreKeysAsUploaded(context.Background(), 11); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if call.batchID != strings.Repeat("b", 32) || !equalIDs(call.ids, []uint32{10, 11}) {
			t.Fatalf("ACK authority changed: %v", calls)
		}
	}
}

func TestRemotePreKeyUploadRejectsMalformedResponsesWithoutFreshFallback(t *testing.T) {
	valid := func() *RemotePreKeyUploadBatch {
		return &RemotePreKeyUploadBatch{BatchID: strings.Repeat("a", 32), PreKeys: []*keys.PreKey{publicTestPreKey(1)}}
	}
	cases := map[string]func() *RemotePreKeyUploadBatch{
		"nil":             func() *RemotePreKeyUploadBatch { return nil },
		"empty token":     func() *RemotePreKeyUploadBatch { b := valid(); b.BatchID = ""; return b },
		"uppercase token": func() *RemotePreKeyUploadBatch { b := valid(); b.BatchID = strings.Repeat("A", 32); return b },
		"foreign token":   func() *RemotePreKeyUploadBatch { b := valid(); b.BatchID = strings.Repeat("sensitive", 4); return b },
		"empty":           func() *RemotePreKeyUploadBatch { b := valid(); b.PreKeys = nil; return b },
		"too many": func() *RemotePreKeyUploadBatch {
			b := valid()
			for i := uint32(2); i <= 813; i++ {
				b.PreKeys = append(b.PreKeys, publicTestPreKey(i))
			}
			return b
		},
		"nil key":        func() *RemotePreKeyUploadBatch { b := valid(); b.PreKeys[0] = nil; return b },
		"missing public": func() *RemotePreKeyUploadBatch { b := valid(); b.PreKeys[0].Pub = nil; return b },
		"zero public":    func() *RemotePreKeyUploadBatch { b := valid(); b.PreKeys[0].Pub = new([32]byte); return b },
		"private key":    func() *RemotePreKeyUploadBatch { b := valid(); b.PreKeys[0].Priv = new([32]byte); return b },
		"signed key":     func() *RemotePreKeyUploadBatch { b := valid(); b.PreKeys[0].Signature = new([64]byte); return b },
		"zero ID":        func() *RemotePreKeyUploadBatch { b := valid(); b.PreKeys[0].KeyID = 0; return b },
		"wide ID":        func() *RemotePreKeyUploadBatch { b := valid(); b.PreKeys[0].KeyID = 0x1000000; return b },
		"duplicate": func() *RemotePreKeyUploadBatch {
			b := valid()
			b.PreKeys = append(b.PreKeys, publicTestPreKey(1))
			return b
		},
	}
	for name, makeBatch := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeRemoteSignal{}
			remote := &boundaryPreKeyRemote{fakeRemoteSignal: fake, prepared: makeBatch(), ack: func(string, []uint32) error { t.Fatal("invalid batch acknowledged"); return nil }}
			pk := &remotePreKeyStore{remote: remote}
			if _, err := pk.GetOrGenPreKeys(context.Background(), 50); err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("invalid batch error=%v", err)
			}
			if err := pk.MarkPreKeysAsUploaded(context.Background(), 1); err == nil {
				t.Fatal("invalid response became usable")
			}
			if len(fake.marked) != 0 || fake.nextID != 0 {
				t.Fatal("invalid prepare fell back to fresh generation/marking")
			}
		})
	}
	for _, count := range []uint32{0, 813, ^uint32(0)} {
		fake := &fakeRemoteSignal{}
		if _, err := (&remotePreKeyStore{remote: fake}).GetOrGenPreKeys(context.Background(), count); err == nil || len(fake.prepares) != 0 {
			t.Fatalf("invalid count %d reached remote", count)
		}
	}
	remoteErr := errors.New("unsupported prepare operation")
	remote := &boundaryPreKeyRemote{fakeRemoteSignal: &fakeRemoteSignal{}, prepareErr: remoteErr}
	if _, err := (&remotePreKeyStore{remote: remote}).GetOrGenPreKeys(context.Background(), 50); !errors.Is(err, remoteErr) {
		t.Fatal("unsupported prepare did not fail closed")
	}
}

func TestRemoteFreshRetryPreKeyRejectsMalformedMaterial(t *testing.T) {
	for _, generated := range [][]*keys.PreKey{nil, {nil}, {publicTestPreKey(0)}, {publicTestPreKey(1), publicTestPreKey(2)}} {
		fake := &fakeRemoteSignal{}
		remote := &boundaryPreKeyRemote{fakeRemoteSignal: fake, fresh: generated}
		if _, err := (&remotePreKeyStore{remote: remote}).GenOnePreKey(context.Background()); err == nil || len(fake.marked) != 0 {
			t.Fatal("malformed fresh key was returned or marked")
		}
	}
}

func TestRemoteFreshRetryCannotBorrowFromPreparedUpload(t *testing.T) {
	fake := &fakeRemoteSignal{}
	remote := &boundaryPreKeyRemote{fakeRemoteSignal: fake,
		prepared: &RemotePreKeyUploadBatch{BatchID: strings.Repeat("a", 32), PreKeys: []*keys.PreKey{publicTestPreKey(1)}},
		fresh:    []*keys.PreKey{publicTestPreKey(1)},
	}
	pk := &remotePreKeyStore{remote: remote}
	if _, err := pk.GetOrGenPreKeys(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if _, err := pk.GenOnePreKey(context.Background()); err == nil || len(fake.marked) != 0 {
		t.Fatal("pending upload key was accepted as a fresh retry key")
	}
}

func installUploadIQ(t *testing.T, cli *Client, remote *fakeRemoteSignal, failure string, want int) {
	t.Helper()
	cli.testSendHook = func(raw []byte) error {
		unpacked, err := waBinary.Unpack(raw)
		if err != nil {
			return err
		}
		node, err := waBinary.Unmarshal(unpacked)
		if err != nil {
			return err
		}
		if node.Tag != "iq" || node.AttrGetter().OptionalString("xmlns") != "encrypt" || node.AttrGetter().OptionalString("type") != "set" {
			return errors.New("unexpected upload IQ")
		}
		listNode := node.GetChildByTag("list")
		list := listNode.GetChildren()
		if len(list) != want {
			t.Errorf("IQ keys=%d, want%d", len(list), want)
		}
		remote.mu.Lock()
		remote.operations = append(remote.operations, "iq")
		for i, item := range list {
			id, ok := item.GetChildByTag("id").Content.([]byte)
			if !ok || len(id) != 3 || i >= len(remote.batch.PreKeys) || binary.BigEndian.Uint32(append([]byte{0}, id...)) != remote.batch.PreKeys[i].KeyID {
				t.Error("IQ did not contain the complete prepared ordered public batch")
			}
		}
		if len(remote.acks) != 0 {
			t.Error("batch was acknowledged before successful IQ")
		}
		remote.mu.Unlock()
		if failure == "transport" {
			return errors.New("synthetic IQ transport failure")
		}
		response := &waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{"id": node.AttrGetter().OptionalString("id"), "type": "result"}}
		if failure == "server" {
			response.Attrs["type"] = "error"
			response.Content = []waBinary.Node{{Tag: "error", Attrs: waBinary.Attrs{"code": "500", "text": "synthetic"}}}
		}
		if !cli.receiveResponse(context.Background(), response) {
			t.Error("upload IQ response had no registered waiter")
		}
		return nil
	}
}

func TestRemoteActualUploadIQFailureNeverAcknowledgesAndReopenRecovers812(t *testing.T) {
	cli, remote, _ := newTestRemote(t)
	installUploadIQ(t, cli, remote, "transport", 812)
	cli.uploadPreKeys(context.Background(), true)
	if remote.batch == nil || len(remote.batch.PreKeys) != 812 || len(remote.acks) != 0 || len(remote.marked) != 0 {
		t.Fatal("failed IQ lost or acknowledged prepared keys")
	}
	// A different client/store instance simulates reopening against the same
	// durable remote state. A normal50 request still uploads the full812.
	second := NewRemoteSignalClient(&store.Device{}, testRemoteIdentity, remote, nil)
	installUploadIQ(t, second, remote, "", 812)
	second.uploadPreKeys(context.Background(), false)
	if len(remote.prepares) != 2 || remote.prepares[0] != 812 || remote.prepares[1] != 50 || len(remote.acks) != 1 || len(remote.acks[0].ids) != 812 || remote.nextID != 812 || len(remote.marked) != 0 {
		t.Fatalf("prepares=%v ACKs=%d allocated=%d", remote.prepares, len(remote.acks), remote.nextID)
	}
}

func TestRemoteActualUploadIQServerErrorDoesNotAcknowledgeOnPrepare(t *testing.T) {
	cli, remote, _ := newTestRemote(t)
	installUploadIQ(t, cli, remote, "server", 50)
	cli.uploadPreKeys(context.Background(), false)
	first := remote.batch.BatchID
	batch, err := cli.Store.PreKeys.GetOrGenPreKeys(context.Background(), 812)
	if err != nil || len(batch) != 50 || remote.batch.BatchID != first || len(remote.acks) != 0 || len(remote.marked) != 0 || remote.nextID != 50 {
		t.Fatalf("failed IQ prepare len=%d err=%v ACKs=%v", len(batch), err, remote.acks)
	}
}

func TestRemoteLostACKIsReplayedBeforePreparingAnotherBatch(t *testing.T) {
	cli, remote, _ := newTestRemote(t)
	remote.ackLost = true
	installUploadIQ(t, cli, remote, "", 50)
	cli.uploadPreKeys(context.Background(), false)
	if remote.batch != nil || len(remote.acks) != 1 || !cli.lastPreKeyUpload.IsZero() {
		t.Fatal("lost ACK fixture did not apply the first batch before response loss")
	}
	pk := cli.Store.PreKeys.(*remotePreKeyStore)
	if _, err := pk.GetOrGenPreKeys(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	if len(remote.acks) != 2 || remote.acks[0].batchID != remote.acks[1].batchID || !equalIDs(remote.acks[0].ids, remote.acks[1].ids) || remote.batch == nil || remote.batch.BatchID == remote.acks[0].batchID || remote.nextID != 100 {
		t.Fatalf("lost ACK was not resolved before new prepare: acks=%v", remote.acks)
	}
	if strings.Join(remote.operations, ",") != "prepare,iq,ack,ack,prepare" {
		t.Fatalf("lost ACK replay ordering=%v", remote.operations)
	}
	// A persistent ACK failure must not permit even a prepare allocation.
	batch, err := pk.GetOrGenPreKeys(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	remote.ackErr = errors.New("synthetic unavailable ACK")
	if err = pk.MarkPreKeysAsUploaded(context.Background(), batch[len(batch)-1].KeyID); err == nil {
		t.Fatal("ACK failure hidden")
	}
	prepares := len(remote.prepares)
	if _, err = pk.GetOrGenPreKeys(context.Background(), 50); err == nil || len(remote.prepares) != prepares || remote.nextID != 100 {
		t.Fatal("prepare replaced an unresolved attempted ACK")
	}
}

type serializedPreKeyRemote struct {
	*fakeRemoteSignal
	active atomic.Int32
	max    atomic.Int32
}

func (r *serializedPreKeyRemote) enter() func() {
	n := r.active.Add(1)
	for previous := r.max.Load(); n > previous; previous = r.max.Load() {
		if r.max.CompareAndSwap(previous, n) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	return func() { r.active.Add(-1) }
}

func (r *serializedPreKeyRemote) PreparePreKeyUpload(ctx context.Context, count uint32) (*RemotePreKeyUploadBatch, error) {
	defer r.enter()()
	return r.fakeRemoteSignal.PreparePreKeyUpload(ctx, count)
}
func (r *serializedPreKeyRemote) AcknowledgePreKeyUpload(ctx context.Context, token string, ids []uint32) error {
	defer r.enter()()
	return r.fakeRemoteSignal.AcknowledgePreKeyUpload(ctx, token, ids)
}
func (r *serializedPreKeyRemote) GeneratePreKeys(ctx context.Context, count uint32) ([]*keys.PreKey, error) {
	defer r.enter()()
	return r.fakeRemoteSignal.GeneratePreKeys(ctx, count)
}
func (r *serializedPreKeyRemote) MarkPreKeysUploaded(ctx context.Context, ids []uint32) error {
	defer r.enter()()
	return r.fakeRemoteSignal.MarkPreKeysUploaded(ctx, ids)
}

func TestRemotePreKeyUploadSerializesPrepareMarkAndFreshGeneration(t *testing.T) {
	remote := &serializedPreKeyRemote{fakeRemoteSignal: &fakeRemoteSignal{}}
	pk := &remotePreKeyStore{remote: remote}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			for j := 0; j < 4; j++ {
				batch, err := pk.GetOrGenPreKeys(context.Background(), 3)
				if err != nil {
					t.Error(err)
					return
				}
				// Other complete callers may have retired this snapshot, so a
				// literal-last mismatch is expected and must perform zero ACK.
				_ = pk.MarkPreKeysAsUploaded(context.Background(), batch[len(batch)-1].KeyID)
				if _, err := pk.GenOnePreKey(context.Background()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if remote.max.Load() != 1 || len(remote.marked) != 48 || len(remote.acks) == 0 {
		t.Fatalf("parallel calls=%d fresh=%d ACK=%d", remote.max.Load(), len(remote.marked), len(remote.acks))
	}
}
