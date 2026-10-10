// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/libsignal-protocol-go/ecc"
	"github.com/polymorfa/libsignal-protocol-go/keys/prekey"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waAdv"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	"github.com/polymorfa/hypermeow/util/keys"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

// RemoteSignal holds a linked device's Signal private keys, sessions and
// message plaintext outside this client.
//
// A client created with [NewRemoteSignalClient] keeps its socket, receipts,
// retries, prekey uploads and pairing flow, but never decrypts or encrypts a
// message itself and never sees plaintext. Every operation that needs a
// private key goes through this interface. Outgoing messages arrive as
// finished stanzas built by the remote store; see [Client.SendPrebuiltStanza].
type RemoteSignal interface {
	// Receive hands one `<enc>` payload to the remote store, which decrypts,
	// processes and delivers it to the application. Only delivery metadata
	// comes back. A decryption failure is reported as *RemoteDecryptError.
	Receive(ctx context.Context, req RemoteReceive) (*RemoteReceiveResult, error)
	// GeneratePreKeys creates one-time prekeys. Only public halves and IDs
	// are returned; private halves stay remote.
	GeneratePreKeys(ctx context.Context, count uint32) ([]*keys.PreKey, error)
	// MarkPreKeysUploaded records that these prekeys reached the server.
	MarkPreKeysUploaded(ctx context.Context, ids []uint32) error
	// UploadedPreKeyCount reports how many uploaded prekeys remain unused.
	UploadedPreKeyCount(ctx context.Context) (int, error)
	// SignPairing verifies the primary's pair-success container and returns
	// the encoded ADVSignedDeviceIdentity with this device's signature.
	SignPairing(ctx context.Context, deviceIdentityContainer []byte) ([]byte, error)
	// SetOwnJIDs tells the remote store this device's JIDs after pairing.
	SetOwnJIDs(ctx context.Context, pn, lid types.JID) error
	// Resend answers a retry receipt with a re-encrypted stanza for the
	// requester, built from the remote store's own record of the message.
	Resend(ctx context.Context, req RemoteResend) ([]byte, error)
	// ForgetIdentity drops the stored identity and sessions for every device
	// of user after the server reports an identity change.
	ForgetIdentity(ctx context.Context, user types.JID) error
	// MigratePNToLID moves the sessions and identities of a phone-number
	// user's devices, including hosted devices, to the user's LID. Calling it
	// again after a successful migration is a no-op.
	MigratePNToLID(ctx context.Context, pn, lid types.JID) error
}

// RemoteIdentity is the public material a remote-keyed device registers with.
type RemoteIdentity struct {
	RegistrationID        uint32
	IdentityKey           [32]byte
	SignedPreKeyID        uint32
	SignedPreKey          [32]byte
	SignedPreKeySignature [64]byte
	// AdvSecret is the pairing HMAC key shared with the primary through the
	// QR code. It authenticates pairing; it decrypts nothing.
	AdvSecret [32]byte
}

// RemoteReceive is one `<enc>` payload of a received message.
type RemoteReceive struct {
	Chat           types.JID
	Sender         types.JID
	MessageID      types.MessageID
	Kind           string // "pkmsg", "msg" or "skmsg"
	Ciphertext     []byte
	PaddingVersion int
	IsFromMe       bool
}

// RemoteReceiveResult is what the client learns about a received payload.
type RemoteReceiveResult struct {
	// AlreadyDelivered means the application has had this payload before;
	// the client only needs to acknowledge it again.
	AlreadyDelivered bool
	// Redelivered means the payload was served from the remote buffer.
	Redelivered bool
	// SenderKeyRejected means a sender-key distribution in the message could
	// not be stored.
	SenderKeyRejected bool
	// AppStateKeyShare is set only for shares from this account's own
	// devices; it is the one piece of content the client receives.
	AppStateKeyShare *waE2E.AppStateSyncKeyShare
}

// RemoteDecryptFailure classifies why the remote store could not decrypt.
type RemoteDecryptFailure int

const (
	// RemoteDecryptOther is any failure that should trigger a retry receipt.
	RemoteDecryptOther RemoteDecryptFailure = iota
	// RemoteDecryptOldCounter is a message older than the session can decrypt.
	RemoteDecryptOldCounter
	// RemoteDecryptNoSenderKey is a group message with no sender key.
	RemoteDecryptNoSenderKey
	// RemoteDecryptUndecodable decrypted but did not decode as a message.
	RemoteDecryptUndecodable
)

// RemoteDecryptError is returned by [RemoteSignal.Receive] for payloads that
// could not be decrypted.
type RemoteDecryptError struct {
	Failure RemoteDecryptFailure
	Err     error
}

func (e *RemoteDecryptError) Error() string {
	return fmt.Sprintf("remote decrypt failed (%d): %v", e.Failure, e.Err)
}

func (e *RemoteDecryptError) Unwrap() error { return e.Err }

// RemoteResend asks the remote store to re-encrypt a sent message.
type RemoteResend struct {
	Chat          types.JID
	MessageID     types.MessageID
	Requester     types.JID
	EncryptionJID types.JID
	// Recipient is the original chat user when the requester is one of this
	// account's own devices on a direct message.
	Recipient types.JID
	IsGroup   bool
	// IsBroadcast marks a broadcast-list retry; group addressing is unset.
	IsBroadcast    bool
	AddressingMode types.AddressingMode
	RetryCount     int
	// Bundle carries keys from the retry receipt, used to start a fresh
	// session. Nil when the receipt had none.
	Bundle *prekey.Bundle
}

// ErrRemoteSignalUnsupported is returned for flows a remote-keyed client
// cannot run yet.
var ErrRemoteSignalUnsupported = errors.New("operation is not supported with remote Signal keys")

// ErrRemoteSignalRequired is returned for operations that only a remote-keyed
// client performs.
var ErrRemoteSignalRequired = errors.New("operation requires a client with remote Signal keys")

// NewRemoteSignalClient constructs a client whose Signal private keys live in
// remote. The device store holds only public material: private key fields
// are zero and the device is marked [store.Device.RemoteKeys], so a local
// Signal path reached by mistake fails loudly instead of using zero keys.
// Persisting the device stores those zero fields; the caller sets the same
// identity again when it loads a remote-keyed device.
func NewRemoteSignalClient(deviceStore *store.Device, identity RemoteIdentity, remote RemoteSignal, log waLog.Logger) *Client {
	if remote == nil || isTypedNil(remote) {
		panic("whatsmeow: NewRemoteSignalClient requires a non-nil RemoteSignal")
	}
	if deviceStore == nil {
		panic("whatsmeow: NewRemoteSignalClient requires a non-nil device store")
	}
	applyRemoteIdentity(deviceStore, identity, remote)
	cli := NewClient(deviceStore, log)
	cli.remoteSignal = remote
	return cli
}

func applyRemoteIdentity(device *store.Device, identity RemoteIdentity, remote RemoteSignal) {
	identityPub, signedPreKeyPub, signature := identity.IdentityKey, identity.SignedPreKey, identity.SignedPreKeySignature
	device.RegistrationID = identity.RegistrationID
	device.IdentityKey = &keys.KeyPair{Pub: &identityPub, Priv: new([32]byte)}
	device.SignedPreKey = &keys.PreKey{
		KeyPair:   keys.KeyPair{Pub: &signedPreKeyPub, Priv: new([32]byte)},
		KeyID:     identity.SignedPreKeyID,
		Signature: &signature,
	}
	device.AdvSecretKey = append([]byte(nil), identity.AdvSecret[:]...)
	device.PreKeys = &remotePreKeyStore{remote: remote}
	device.RemoteKeys = true
}

// isRemoteSignal reports whether this client's Signal keys are remote.
func (cli *Client) isRemoteSignal() bool {
	return cli != nil && cli.remoteSignal != nil
}

// remotePreKeyStore serves prekey uploads and retry receipts from the remote
// store. Only public halves pass through it.
type remotePreKeyStore struct {
	remote RemoteSignal

	mu sync.Mutex
	// pending holds keys handed out for upload but not yet marked uploaded,
	// oldest first. A failed upload leaves them here to be offered again.
	pending []*keys.PreKey
	// lastBatch is the set GetOrGenPreKeys returned most recently, the only
	// keys MarkPreKeysAsUploaded may mark.
	lastBatch map[uint32]struct{}
}

var _ store.PreKeyStore = (*remotePreKeyStore)(nil)

// GetOrGenPreKeys returns still-unuploaded keys first and generates only the
// rest, as the SQL store does, so a failed upload never strands a batch.
func (s *remotePreKeyStore) GetOrGenPreKeys(ctx context.Context, count uint32) ([]*keys.PreKey, error) {
	s.mu.Lock()
	batch := append([]*keys.PreKey(nil), s.pending[:min(int(count), len(s.pending))]...)
	s.mu.Unlock()
	if missing := int(count) - len(batch); missing > 0 {
		generated, err := s.remote.GeneratePreKeys(ctx, uint32(missing))
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.pending = append(s.pending, generated...)
		s.mu.Unlock()
		batch = append(batch, generated...)
	}
	ids := make(map[uint32]struct{}, len(batch))
	for _, key := range batch {
		ids[key.KeyID] = struct{}{}
	}
	s.mu.Lock()
	s.lastBatch = ids
	s.mu.Unlock()
	return batch, nil
}

// GenOnePreKey creates a key for a retry receipt. The receipt publishes it,
// so it is marked uploaded at once, as the SQL store does.
func (s *remotePreKeyStore) GenOnePreKey(ctx context.Context) (*keys.PreKey, error) {
	generated, err := s.remote.GeneratePreKeys(ctx, 1)
	if err != nil {
		return nil, err
	}
	if len(generated) != 1 {
		return nil, fmt.Errorf("remote store returned %d prekeys, want 1", len(generated))
	}
	if err = s.remote.MarkPreKeysUploaded(ctx, []uint32{generated[0].KeyID}); err != nil {
		return nil, fmt.Errorf("failed to mark retry prekey as published: %w", err)
	}
	return generated[0], nil
}

func (s *remotePreKeyStore) GetPreKey(ctx context.Context, id uint32) (*keys.PreKey, error) {
	return nil, fmt.Errorf("GetPreKey(%d): %w", id, ErrRemoteSignalUnsupported)
}

func (s *remotePreKeyStore) RemovePreKey(ctx context.Context, id uint32) error {
	return fmt.Errorf("RemovePreKey(%d): %w", id, ErrRemoteSignalUnsupported)
}

// MarkPreKeysAsUploaded marks the keys of the most recent GetOrGenPreKeys
// batch with an ID up to upToID. Prekey IDs wrap at 2^24, but one upload
// batch never spans the wrap in practice, so a plain comparison is enough.
func (s *remotePreKeyStore) MarkPreKeysAsUploaded(ctx context.Context, upToID uint32) error {
	s.mu.Lock()
	var marked []uint32
	for _, key := range s.pending {
		if _, inBatch := s.lastBatch[key.KeyID]; inBatch && key.KeyID <= upToID {
			marked = append(marked, key.KeyID)
		}
	}
	s.mu.Unlock()
	if len(marked) == 0 {
		return nil
	}
	if err := s.remote.MarkPreKeysUploaded(ctx, marked); err != nil {
		return err
	}
	done := make(map[uint32]struct{}, len(marked))
	for _, id := range marked {
		done[id] = struct{}{}
	}
	s.mu.Lock()
	kept := s.pending[:0]
	for _, key := range s.pending {
		if _, ok := done[key.KeyID]; !ok {
			kept = append(kept, key)
		}
	}
	s.pending = kept
	s.mu.Unlock()
	return nil
}

func (s *remotePreKeyStore) UploadedPreKeyCount(ctx context.Context) (int, error) {
	return s.remote.UploadedPreKeyCount(ctx)
}

// decryptMessagesRemote is decryptMessages for a remote-keyed client: each
// `<enc>` payload goes to the remote store, and only delivery metadata comes
// back. Receipts, retry receipts and acks follow the local path.
func (cli *Client) decryptMessagesRemote(ctx context.Context, info *types.MessageInfo, node *waBinary.Node, senderEncryptionJID types.JID) {
	var recognizedStanza, undecodable, containsDirectMsg bool
	for _, child := range node.GetChildren() {
		if child.Tag != "enc" {
			continue
		}
		recognizedStanza = true
		ag := child.AttrGetter()
		encType, ok := ag.GetString("type", false)
		if !ok {
			continue
		}
		switch {
		case encType == "pkmsg" || encType == "msg":
			containsDirectMsg = true
		case encType == "skmsg" && info.IsGroup:
		case encType == "msmsg":
			// Bot message secrets are kept by the remote store, which this
			// path does not reach yet.
			cli.backgroundIfAsyncAck(func() {
				cli.sendAck(ctx, node, NackMissingMessageSecret)
			})
			return
		default:
			cli.Log.Warnf("Unhandled encrypted message (type %s) from %s", encType, info.SourceString())
			continue
		}
		ciphertext, ok := child.Content.([]byte)
		if !ok {
			cli.Log.Warnf("Encrypted payload from %s is not a byte slice", info.SourceString())
			continue
		}
		result, err := cli.remoteSignal.Receive(ctx, RemoteReceive{
			Chat:           info.Chat,
			Sender:         senderEncryptionJID,
			MessageID:      info.ID,
			Kind:           encType,
			Ciphertext:     ciphertext,
			PaddingVersion: ag.OptionalInt("v"),
			IsFromMe:       info.IsFromMe,
		})
		var decryptErr *RemoteDecryptError
		isDecryptErr := errors.As(err, &decryptErr)
		switch {
		case err == nil:
		case isDecryptErr && decryptErr.Failure == RemoteDecryptOldCounter:
			cli.Log.Warnf("Ignoring message %s from %s: %v", info.ID, info.SourceString(), err)
			continue
		case isDecryptErr && decryptErr.Failure == RemoteDecryptUndecodable:
			cli.Log.Warnf("Remote store could not decode message %s from %s: %v", info.ID, info.SourceString(), err)
			undecodable = true
			continue
		case !isDecryptErr:
			// The remote store may never have seen the payload (outage,
			// timeout). Neither ack nor send a retry receipt: the server
			// redelivers the stanza, and acknowledging it could lose the
			// message for good.
			cli.Log.Errorf("Remote Signal store unavailable for message %s from %s: %v", info.ID, info.SourceString(), err)
			return
		default:
			cli.Log.Warnf("Error decrypting message %s from %s remotely: %v", info.ID, info.SourceString(), err)
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			isUnavailable := encType == "skmsg" && !containsDirectMsg &&
				isDecryptErr && decryptErr.Failure == RemoteDecryptNoSenderKey
			if cli.SynchronousAck {
				cli.sendRetryReceipt(ctx, node, info, isUnavailable)
				cli.sendAck(ctx, node, 0)
			} else {
				go cli.sendRetryReceipt(context.WithoutCancel(ctx), node, info, isUnavailable)
				go cli.sendAck(ctx, node, 0)
			}
			cli.dispatchEvent(&events.UndecryptableMessage{
				Info:            *info,
				IsUnavailable:   isUnavailable,
				DecryptFailMode: events.DecryptFailMode(ag.OptionalString("decrypt-fail")),
			})
			return
		}
		undecodable = false
		cli.cancelDelayedRequestFromPhone(info.ID)
		if result.AppStateKeyShare != nil {
			cli.handleAppStateSyncKeyShare(ctx, result.AppStateKeyShare)
		}
		if !result.AlreadyDelivered {
			cli.dispatchEvent(&events.RemoteMessage{
				Info:              *info,
				Redelivered:       result.Redelivered,
				SenderKeyRejected: result.SenderKeyRejected,
			})
		}
	}
	cli.backgroundIfAsyncAck(func() {
		if !recognizedStanza {
			cli.sendAck(ctx, node, NackUnrecognizedStanza)
		} else if undecodable {
			cli.sendAck(ctx, node, NackInvalidProtobuf)
		} else {
			cli.sendMessageReceipt(ctx, info, node)
		}
	})
}

// remoteDeviceSignature asks the remote store to sign the pairing identity.
// The store verifies the container itself and signs only the ADV message it
// builds; the client still checks that the result covers the details it
// verified locally.
func (cli *Client) remoteDeviceSignature(ctx context.Context, container []byte, verified *waAdv.ADVSignedDeviceIdentity) ([]byte, error) {
	signed, err := cli.remoteSignal.SignPairing(ctx, container)
	if err != nil {
		return nil, fmt.Errorf("remote Signal store refused pairing: %w", err)
	}
	var identity waAdv.ADVSignedDeviceIdentity
	if err = proto.Unmarshal(signed, &identity); err != nil {
		return nil, &PairProtoError{"failed to parse remotely signed device identity", err}
	}
	if !bytes.Equal(identity.GetDetails(), verified.GetDetails()) {
		return nil, fmt.Errorf("remote Signal store signed different device identity details")
	}
	if len(identity.GetDeviceSignature()) != 64 {
		return nil, fmt.Errorf("remote Signal store returned a %d-byte device signature", len(identity.GetDeviceSignature()))
	}
	// Check it is a valid signature by the advertised identity key over the
	// exact message generateDeviceSignature signs.
	message := concatBytes(AdvDeviceSignaturePrefix, verified.Details, cli.Store.IdentityKey.Pub[:], verified.AccountSignatureKey)
	signature := *(*[64]byte)(identity.GetDeviceSignature())
	if !ecc.VerifySignature(ecc.NewDjbECPublicKey(*cli.Store.IdentityKey.Pub), message, signature) {
		return nil, fmt.Errorf("remote Signal store returned a device signature that does not verify")
	}
	return identity.GetDeviceSignature(), nil
}

// handleRetryReceiptRemote answers a retry receipt with a stanza the remote
// store re-encrypts from its own record of the message.
func (cli *Client) handleRetryReceiptRemote(ctx context.Context, receipt *events.Receipt, node *waBinary.Node, messageID types.MessageID, retryCount int) error {
	if !cli.acceptIncomingRetry(receipt.Sender, messageID) {
		return nil
	}
	// The remote store holds the message, so the callback gets no content.
	if cli.PreRetryCallback != nil && !cli.PreRetryCallback(receipt, messageID, retryCount, nil) {
		cli.Log.Debugf("Cancelled retry receipt in PreRetryCallback")
		return nil
	}
	req := RemoteResend{
		Chat:          receipt.Chat,
		MessageID:     messageID,
		Requester:     receipt.Sender,
		EncryptionJID: receipt.Sender,
		RetryCount:    retryCount,
	}
	if _, hasKeys := node.GetOptionalChildByTag("keys"); hasKeys {
		bundle, err := nodeToPreKeyBundle(uint32(receipt.Sender.Device), *node)
		if err != nil {
			return fmt.Errorf("failed to read prekey bundle in retry receipt: %w", err)
		}
		req.Bundle = bundle
	}
	if receipt.Sender.Server == types.DefaultUserServer {
		if lid, err := cli.Store.LIDs.GetLIDForPN(ctx, receipt.Sender); err != nil {
			cli.Log.Warnf("Failed to get LID for %s: %v", receipt.Sender, err)
		} else if !lid.IsEmpty() {
			req.EncryptionJID = lid
		}
	}
	switch {
	case receipt.Chat.Server == types.BroadcastServer:
		req.IsBroadcast = true
	case receipt.IsGroup:
		req.IsGroup = true
		if group, err := cli.getCachedGroupData(ctx, receipt.Chat); err == nil && group != nil {
			req.AddressingMode = group.AddressingMode
		}
	case receipt.IsFromMe:
		req.Recipient = receipt.Chat
	}
	stanza, err := cli.remoteSignal.Resend(ctx, req)
	if err != nil {
		return fmt.Errorf("remote Signal store could not resend %s: %w", messageID, err)
	}
	return cli.sendRemoteStanza(ctx, stanza)
}

func decodeRemoteStanza(stanza []byte) (*waBinary.Node, error) {
	unpacked, err := waBinary.Unpack(stanza)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack remote stanza: %w", err)
	}
	node, err := waBinary.Unmarshal(unpacked)
	if err != nil {
		return nil, fmt.Errorf("failed to decode remote stanza: %w", err)
	}
	if node.Tag != "message" {
		return nil, fmt.Errorf("remote stanza is <%s>, want <message>", node.Tag)
	}
	return node, nil
}

func (cli *Client) sendRemoteStanza(ctx context.Context, stanza []byte) error {
	node, err := decodeRemoteStanza(stanza)
	if err != nil {
		return err
	}
	return cli.sendNode(ctx, *node)
}

// SendPrebuiltStanza transmits a `<message>` stanza the remote Signal store
// built and waits for the server's ack. phash is the participant hash the
// store computed, compared with the ack as SendMessage does. A zero timeout
// waits until ctx ends.
func (cli *Client) SendPrebuiltStanza(ctx context.Context, stanza []byte, phash string, timeout time.Duration) (SendResponse, error) {
	var resp SendResponse
	if !cli.isRemoteSignal() {
		return resp, fmt.Errorf("SendPrebuiltStanza: %w", ErrRemoteSignalRequired)
	}
	node, err := decodeRemoteStanza(stanza)
	if err != nil {
		return resp, err
	}
	id, _ := node.Attrs["id"].(string)
	if id == "" {
		return resp, fmt.Errorf("remote stanza has no id")
	}
	resp.ID = types.MessageID(id)
	respChan := cli.waitResponse(id)
	data, err := cli.sendNodeAndGetData(ctx, *node)
	if err != nil {
		cli.cancelResponse(id, respChan)
		return resp, err
	}
	var timeoutChan <-chan time.Time
	if timeout > 0 {
		timeoutChan = time.After(timeout)
	}
	var respNode *waBinary.Node
	select {
	case respNode = <-respChan:
	case <-timeoutChan:
		cli.cancelResponse(id, respChan)
		return resp, ErrMessageTimedOut
	case <-ctx.Done():
		cli.cancelResponse(id, respChan)
		return resp, ctx.Err()
	}
	if isDisconnectNode(respNode) {
		if respNode, err = cli.retryFrame(ctx, "prebuilt message send", id, data, respNode, 0); err != nil {
			return resp, err
		}
	}
	ag := respNode.AttrGetter()
	resp.ServerID = types.MessageServerID(ag.OptionalInt("server_id"))
	resp.Timestamp = ag.UnixTime("t")
	if errorCode := ag.Int("error"); errorCode != 0 {
		return resp, fmt.Errorf("%w %d", ErrServerReturnedError, errorCode)
	}
	setParticipantHashMismatch(&resp, phash, ag.OptionalString("phash"))
	return resp, nil
}

// forgetRemoteIdentity asks the remote store to drop identity and session
// state for from and its PN/LID alternate, mirroring deleteIdentityChangeState.
// The store clears a phone-number or LID user's hosted devices with it, so a
// hosted JID is sent as its base user.
func (cli *Client) forgetRemoteIdentity(ctx context.Context, from types.JID) {
	base := from.ToNonAD()
	switch base.Server {
	case types.HostedServer:
		base.Server = types.DefaultUserServer
	case types.HostedLIDServer:
		base.Server = types.HiddenUserServer
	}
	users := []types.JID{base}
	if cli.Store.LIDs != nil {
		var alternate types.JID
		var err error
		switch from.Server {
		case types.DefaultUserServer, types.HostedServer:
			alternate, err = cli.Store.LIDs.GetLIDForPN(ctx, types.NewJID(from.User, types.DefaultUserServer))
		case types.HiddenUserServer, types.HostedLIDServer:
			alternate, err = cli.Store.LIDs.GetPNForLID(ctx, types.NewJID(from.User, types.HiddenUserServer))
		}
		if err != nil {
			cli.Log.Warnf("Failed to resolve alternate JID for identity change from %s: %v", from, err)
		} else if !alternate.IsEmpty() {
			users = append(users, alternate.ToNonAD())
		}
	}
	for _, user := range users {
		if err := cli.remoteSignal.ForgetIdentity(ctx, user); err != nil {
			cli.Log.Warnf("Remote Signal store failed to forget identity of %s: %v", user, err)
		}
	}
}

// migrateRemoteSessions asks the remote store to move pn's Signal state to
// lid, once per user per client, as SQLStore.MigratePNToLID does locally.
func (cli *Client) migrateRemoteSessions(ctx context.Context, pn, lid types.JID) {
	pn, lid = pn.ToNonAD(), lid.ToNonAD()
	if _, done := cli.remoteMigratedPN.Load(pn.User); done {
		return
	}
	if err := cli.remoteSignal.MigratePNToLID(ctx, pn, lid); err != nil {
		cli.Log.Errorf("Remote Signal store failed to migrate %s to %s: %v", pn, lid, err)
		return
	}
	cli.remoteMigratedPN.Store(pn.User, struct{}{})
}
