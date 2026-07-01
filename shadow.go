// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/libsignal/keys/prekey"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// ErrShadowClientNoConnect is returned when Connect (or any code path that
// tries to open a socket) is called on a headless "shadow" client. A shadow
// client has no socket by design: outbound traffic is routed through its
// [ShadowRelay] instead.
var ErrShadowClientNoConnect = errors.New("shadow client cannot open a socket; outbound traffic goes through the relay")

// ErrNilNode is returned by [Client.InjectNode] when passed a nil node.
var ErrNilNode = errors.New("cannot inject nil node")

// ShadowRelay is the pluggable backend that a headless ("shadow") [Client]
// delegates real-session work to.
//
// A shadow client runs whatsmeow's full protocol handling (binary
// (de)coding, stanza dispatch, node handlers, event emission) without a live
// socket and without owning the Signal/session state. Work that a client
// with no transport and no local session keys cannot do itself is handed off
// to the relay:
//
//   - SendNode transmits an already-marshaled binary node to the real
//     transport (the send path substitutes this for a socket write).
//   - The remaining methods are a session/keying oracle: decryption,
//     per-device encryption, prekey fetching, device/user resolution, LID
//     resolution and privacy-token lookup. The headless client consults
//     these where it would otherwise reach into a live Signal session or
//     issue an IQ over the socket.
//
// Implementations may talk to any transport (another process, a remote
// service, an in-memory peer). The interface is defined purely in terms of
// whatsmeow-ecosystem types so the headless client stays embeddable.
type ShadowRelay interface {
	// SendNode transmits an already-marshaled binary node to the real
	// transport. nodeData is the wire payload produced by
	// waBinary.Marshal. It must not be retained after the call returns.
	SendNode(ctx context.Context, nodeData []byte) error

	// DecryptDM decrypts a direct-message child node from the given sender.
	// The return values mirror the library's own decrypt entry point: the
	// plaintext, the 32-byte ciphertext hash (used for the decrypted-event
	// buffer; may be nil), and an error.
	DecryptDM(ctx context.Context, child *waBinary.Node, from types.JID, isPreKey bool) (plaintext []byte, ciphertextHash *[32]byte, err error)

	// EncryptForDevice encrypts plaintext for a single recipient device.
	// The optional bundle is used to establish a session if none exists.
	// The returned node is the `<enc>` stanza; the bool reports whether the
	// device identity must be included alongside it.
	EncryptForDevice(ctx context.Context, plaintext []byte, to types.JID, bundle *prekey.Bundle, extraAttrs waBinary.Attrs) (encNode *waBinary.Node, includeDeviceIdentity bool, err error)

	// FetchPreKeys fetches prekey bundles for the given devices, keyed by
	// device JID. Devices for which no bundle could be fetched are omitted.
	FetchPreKeys(ctx context.Context, devices []types.JID) (map[types.JID]*prekey.Bundle, error)

	// GetUserDevices resolves the device list for the given users. Input is
	// a list of regular JIDs; output is a list of AD (device) JIDs.
	GetUserDevices(ctx context.Context, users []types.JID) ([]types.JID, error)

	// GetUserInfo resolves user info (picture ID, status, verified name,
	// devices) for the given JIDs.
	GetUserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error)

	// ResolveLID resolves the LID for a phone-number JID.
	ResolveLID(ctx context.Context, pn types.JID) (types.JID, error)

	// GetPrivacyToken resolves the privacy token for a user.
	GetPrivacyToken(ctx context.Context, user types.JID) (*store.PrivacyToken, error)
}

// NewShadowClient constructs a headless [Client] driven by injected nodes and
// a pluggable [ShadowRelay], for embedding whatsmeow's protocol handling
// without a live socket.
//
// The returned value is a real *Client: its nodeHandlers map is populated
// exactly like [NewClient], so registering event handlers, injecting nodes
// via [Client.InjectNode], and using [Client.DangerousInternals] all behave
// the same as on a normal client. The differences are:
//
//   - It has no socket, and [Client.Connect] is guarded: any attempt to open
//     one returns [ErrShadowClientNoConnect].
//   - Its Store is the caller-provided seeded snapshot (typically a device
//     with identity/session keys imported from an existing session).
//   - Its send path routes marshaled nodes through relay.SendNode instead of
//     a socket write.
//   - Its Signal/keying entry points (decryption, per-device encryption,
//     prekey fetch, device/user/LID/privacy-token resolution) consult the
//     relay, since a headless client with no live session cannot perform
//     them locally.
//
// deviceStore must be non-nil (it holds the seeded snapshot). relay must be
// non-nil; it is what makes the client a shadow.
func NewShadowClient(deviceStore *store.Device, relay ShadowRelay, log waLog.Logger) *Client {
	cli := NewClient(deviceStore, log)
	cli.shadowRelay = relay
	// The device-store LID and privacy-token lookups are read directly by
	// consumers (Store.LIDs.GetLIDForPN, Store.PrivacyTokens.GetPrivacyToken)
	// and by the send path. Wrap the seeded stores so those reads fall back
	// to the relay oracle when the seeded snapshot has no local answer.
	if deviceStore != nil && relay != nil {
		deviceStore.LIDs = &shadowLIDStore{inner: deviceStore.LIDs, relay: relay}
		deviceStore.PrivacyTokens = &shadowPrivacyTokenStore{inner: deviceStore.PrivacyTokens, relay: relay}
	}
	return cli
}

// isShadow reports whether this client is a headless shadow (has a relay and
// therefore no socket).
func (cli *Client) isShadow() bool {
	return cli != nil && cli.shadowRelay != nil
}

// InjectNode replays the normal receive-loop dispatch for an already-decoded
// binary node, as if it had arrived over a live socket. It runs the
// [RawNodeHandler] hook, the Signal-disabled message handoff, IQ response
// correlation, and the standard tag-based node handlers, in the same order a
// real socket delivery would.
//
// Unlike the live path, dispatch is synchronous: the relevant node handler
// runs (and any resulting events are emitted) before InjectNode returns. This
// is required because a shadow client never starts the background handler
// queue loop (that loop is started by Connect, which a shadow never calls).
//
// It returns an error only for programming mistakes (nil client/node) or a
// failure to parse a message envelope during the Signal-disabled handoff.
// A node with an unknown tag, or one dropped by the RawNodeHandler, is not an
// error.
func (cli *Client) InjectNode(ctx context.Context, node *waBinary.Node) error {
	if cli == nil {
		return ErrClientIsNil
	}
	if node == nil {
		return ErrNilNode
	}
	if h := cli.RawNodeHandler; h != nil {
		modified, drop := h(ctx, node)
		if drop {
			cli.recvLog.Debugf("RawNodeHandler dropped injected node: %s", node.XMLString())
			return nil
		}
		if modified != nil {
			node = modified
		}
	}
	cli.recvLog.Debugf("%s", node.XMLString())
	// Mirror handleFrame's Signal-disabled handoff so injected `<message>`
	// envelopes reach the caller that owns the Signal session.
	if node.Tag == "message" && cli.DisabledFeatures.Signal {
		info, err := cli.parseMessageInfo(node)
		if err != nil {
			return fmt.Errorf("failed to parse injected message for Signal-disabled handoff: %w", err)
		}
		cli.dispatchEvent(&events.UndecryptedMessage{Info: *info, Raw: node})
		return nil
	}
	if node.Tag == "xmlstreamend" {
		return nil
	}
	if cli.receiveResponse(ctx, node) {
		return nil
	}
	if handler, ok := cli.nodeHandlers[node.Tag]; ok {
		// Dispatch synchronously (see doc comment): the shadow has no
		// handler queue loop draining cli.handlerQueue.
		handler(ctx, node)
		return nil
	}
	if node.Tag != "ack" {
		cli.recvLog.Debugf("Didn't handle injected WhatsApp node %s", node.Tag)
	}
	return nil
}

// shadowLIDStore wraps a seeded [store.LIDStore] so LID reads fall back to the
// relay when the local snapshot has no mapping. Writes and reverse lookups go
// to the seeded store when present, otherwise degrade gracefully.
type shadowLIDStore struct {
	inner store.LIDStore
	relay ShadowRelay
}

func (s *shadowLIDStore) GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error) {
	if s.inner != nil {
		if lid, err := s.inner.GetLIDForPN(ctx, pn); err == nil && !lid.IsEmpty() {
			return lid, nil
		}
	}
	return s.relay.ResolveLID(ctx, pn)
}

func (s *shadowLIDStore) GetManyLIDsForPNs(ctx context.Context, pns []types.JID) (map[types.JID]types.JID, error) {
	if s.inner != nil {
		return s.inner.GetManyLIDsForPNs(ctx, pns)
	}
	res := make(map[types.JID]types.JID, len(pns))
	for _, pn := range pns {
		if lid, err := s.relay.ResolveLID(ctx, pn); err == nil && !lid.IsEmpty() {
			res[pn] = lid
		}
	}
	return res, nil
}

func (s *shadowLIDStore) GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error) {
	if s.inner != nil {
		return s.inner.GetPNForLID(ctx, lid)
	}
	return types.EmptyJID, nil
}

func (s *shadowLIDStore) PutManyLIDMappings(ctx context.Context, mappings []store.LIDMapping) error {
	if s.inner != nil {
		return s.inner.PutManyLIDMappings(ctx, mappings)
	}
	return nil
}

func (s *shadowLIDStore) PutLIDMapping(ctx context.Context, lid, jid types.JID) error {
	if s.inner != nil {
		return s.inner.PutLIDMapping(ctx, lid, jid)
	}
	return nil
}

// shadowPrivacyTokenStore wraps a seeded [store.PrivacyTokenStore] so token
// reads fall back to the relay when the local snapshot has no token.
type shadowPrivacyTokenStore struct {
	inner store.PrivacyTokenStore
	relay ShadowRelay
}

func (s *shadowPrivacyTokenStore) GetPrivacyToken(ctx context.Context, user types.JID) (*store.PrivacyToken, error) {
	if s.inner != nil {
		if tok, err := s.inner.GetPrivacyToken(ctx, user); err == nil && tok != nil {
			return tok, nil
		}
	}
	return s.relay.GetPrivacyToken(ctx, user)
}

func (s *shadowPrivacyTokenStore) PutPrivacyTokens(ctx context.Context, tokens ...store.PrivacyToken) error {
	if s.inner != nil {
		return s.inner.PutPrivacyTokens(ctx, tokens...)
	}
	return nil
}

func (s *shadowPrivacyTokenStore) DeleteExpiredPrivacyTokens(ctx context.Context, cutoff time.Time) (int64, error) {
	if s.inner != nil {
		return s.inner.DeleteExpiredPrivacyTokens(ctx, cutoff)
	}
	return 0, nil
}
