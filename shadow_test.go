// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.mau.fi/libsignal/keys/prekey"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// fakeShadowRelay is a test double for [ShadowRelay] that records calls.
type fakeShadowRelay struct {
	mu sync.Mutex

	sentNodes        [][]byte
	decryptDMCalls   int
	encryptCalls     int
	fetchPreKeyCalls int
	userDevicesCalls int
	userInfoCalls    int
	resolveLIDCalls  int
	privacyCalls     int

	sendErr error
}

func (f *fakeShadowRelay) SendNode(ctx context.Context, nodeData []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentNodes = append(f.sentNodes, append([]byte(nil), nodeData...))
	return f.sendErr
}

func (f *fakeShadowRelay) DecryptDM(ctx context.Context, child *waBinary.Node, from types.JID, isPreKey bool) ([]byte, *[32]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decryptDMCalls++
	return []byte("decrypted-by-relay"), nil, nil
}

func (f *fakeShadowRelay) EncryptForDevice(ctx context.Context, plaintext []byte, to types.JID, bundle *prekey.Bundle, extraAttrs waBinary.Attrs) (*waBinary.Node, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.encryptCalls++
	return &waBinary.Node{Tag: "enc"}, false, nil
}

func (f *fakeShadowRelay) FetchPreKeys(ctx context.Context, devices []types.JID) (map[types.JID]*prekey.Bundle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchPreKeyCalls++
	return map[types.JID]*prekey.Bundle{}, nil
}

func (f *fakeShadowRelay) GetUserDevices(ctx context.Context, users []types.JID) ([]types.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userDevicesCalls++
	return users, nil
}

func (f *fakeShadowRelay) GetUserInfo(ctx context.Context, jids []types.JID) (map[types.JID]types.UserInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userInfoCalls++
	return map[types.JID]types.UserInfo{}, nil
}

func (f *fakeShadowRelay) ResolveLID(ctx context.Context, pn types.JID) (types.JID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveLIDCalls++
	return types.EmptyJID, nil
}

func (f *fakeShadowRelay) GetPrivacyToken(ctx context.Context, user types.JID) (*store.PrivacyToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.privacyCalls++
	return nil, nil
}

func (f *fakeShadowRelay) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sentNodes)
}

// Compile-time assertion that the test double satisfies the interface.
var _ ShadowRelay = (*fakeShadowRelay)(nil)

func newTestShadow(t *testing.T, relay ShadowRelay) *Client {
	t.Helper()
	dev := &store.Device{}
	cli := NewShadowClient(dev, relay, nil)
	if cli == nil {
		t.Fatal("NewShadowClient returned nil")
	}
	return cli
}

// TestNewShadowClientPopulatesNodeHandlers verifies the shadow is a real
// *Client whose (unexported) nodeHandlers map is populated exactly like a
// normal client, reached via reflection the way an external consumer would.
func TestNewShadowClientPopulatesNodeHandlers(t *testing.T) {
	shadow := newTestShadow(t, &fakeShadowRelay{})
	normal := NewClient(&store.Device{}, nil)

	shadowHandlers := reflect.ValueOf(shadow).Elem().FieldByName("nodeHandlers")
	if shadowHandlers.Kind() != reflect.Map {
		t.Fatalf("nodeHandlers field is not a map, got %s", shadowHandlers.Kind())
	}
	if shadowHandlers.Len() == 0 {
		t.Fatal("shadow client nodeHandlers map is empty")
	}
	normalHandlers := reflect.ValueOf(normal).Elem().FieldByName("nodeHandlers")
	if shadowHandlers.Len() != normalHandlers.Len() {
		t.Fatalf("shadow nodeHandlers len %d != normal nodeHandlers len %d", shadowHandlers.Len(), normalHandlers.Len())
	}
	// The "call" tag handler is what an injected <call> node dispatches to.
	if _, ok := shadow.nodeHandlers["call"]; !ok {
		t.Fatal("shadow client is missing the \"call\" node handler")
	}
	if shadow.Store != normal.Store && shadow.Store == nil {
		t.Fatal("shadow client Store is nil")
	}
}

// TestShadowConnectIsGuarded verifies a shadow can never open a socket.
func TestShadowConnectIsGuarded(t *testing.T) {
	shadow := newTestShadow(t, &fakeShadowRelay{})
	if err := shadow.Connect(); !errors.Is(err, ErrShadowClientNoConnect) {
		t.Fatalf("Connect() = %v, want ErrShadowClientNoConnect", err)
	}
	if err := shadow.ConnectContext(context.Background()); !errors.Is(err, ErrShadowClientNoConnect) {
		t.Fatalf("ConnectContext() = %v, want ErrShadowClientNoConnect", err)
	}
	if shadow.IsConnected() {
		t.Fatal("shadow client reports connected")
	}
}

// TestShadowInjectNodeDispatchesToHandler verifies InjectNode replays the
// receive-loop dispatch: a synthetic <call> offer reaches both whatsmeow's
// own decoder (which builds the CallOffer event) and the registered handler.
func TestShadowInjectNodeDispatchesToHandler(t *testing.T) {
	relay := &fakeShadowRelay{}
	shadow := newTestShadow(t, relay)
	// Make the deferred ack synchronous so it completes within InjectNode.
	shadow.SynchronousAck = true

	from := types.JID{User: "123456", Server: types.DefaultUserServer}
	creator := types.JID{User: "123456", Server: types.DefaultUserServer}

	var gotOffer *events.CallOffer
	shadow.AddEventHandler(func(evt any) {
		if o, ok := evt.(*events.CallOffer); ok {
			gotOffer = o
		}
	})

	callNode := &waBinary.Node{
		Tag: "call",
		Attrs: waBinary.Attrs{
			"from": from,
			"id":   "callstanza1",
			"t":    "1700000000",
		},
		Content: []waBinary.Node{{
			Tag: "offer",
			Attrs: waBinary.Attrs{
				"call-id":      "callid1",
				"call-creator": creator,
			},
		}},
	}

	if err := shadow.InjectNode(context.Background(), callNode); err != nil {
		t.Fatalf("InjectNode returned error: %v", err)
	}
	if gotOffer == nil {
		t.Fatal("injected <call> node did not dispatch a CallOffer event to the registered handler")
	}
	if gotOffer.CallID != "callid1" {
		t.Fatalf("CallOffer.CallID = %q, want %q", gotOffer.CallID, "callid1")
	}
	// The handler's deferred ack must route to the relay, not a socket.
	if relay.sentCount() == 0 {
		t.Fatal("expected the call handler's ack to be routed to the relay")
	}
}

// TestShadowInjectNilNode verifies the nil-node guard.
func TestShadowInjectNilNode(t *testing.T) {
	shadow := newTestShadow(t, &fakeShadowRelay{})
	if err := shadow.InjectNode(context.Background(), nil); !errors.Is(err, ErrNilNode) {
		t.Fatalf("InjectNode(nil) = %v, want ErrNilNode", err)
	}
}

// TestShadowSendNodeRoutesToRelay verifies that DangerousInternals().SendNode
// on a socketless shadow routes to the relay and never panics on the absent
// socket.
func TestShadowSendNodeRoutesToRelay(t *testing.T) {
	relay := &fakeShadowRelay{}
	shadow := newTestShadow(t, relay)

	node := waBinary.Node{
		Tag:   "iq",
		Attrs: waBinary.Attrs{"id": "req1", "type": "get"},
	}
	if err := shadow.DangerousInternals().SendNode(context.Background(), node); err != nil {
		t.Fatalf("SendNode returned error: %v", err)
	}
	if relay.sentCount() != 1 {
		t.Fatalf("relay recorded %d sends, want 1", relay.sentCount())
	}
}

// TestShadowSendNodeFailsClosedWithoutRelay verifies the fail-closed guard: a
// socketless client with no relay must error, never nil-panic.
func TestShadowSendNodeFailsClosedWithoutRelay(t *testing.T) {
	// A normal client with no socket and no relay.
	cli := NewClient(&store.Device{}, nil)
	err := cli.DangerousInternals().SendNode(context.Background(), waBinary.Node{Tag: "iq"})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("SendNode without socket/relay = %v, want ErrNotConnected", err)
	}
}

// TestShadowSignalOpsDelegateToRelay verifies the Signal/keying entry points
// consult the relay oracle instead of trying to use a socket or local session.
func TestShadowSignalOpsDelegateToRelay(t *testing.T) {
	relay := &fakeShadowRelay{}
	shadow := newTestShadow(t, relay)
	ctx := context.Background()
	jids := []types.JID{{User: "123", Server: types.DefaultUserServer}}

	devices, err := shadow.GetUserDevices(ctx, jids)
	if err != nil {
		t.Fatalf("GetUserDevices returned error: %v", err)
	}
	if relay.userDevicesCalls != 1 {
		t.Fatalf("relay.GetUserDevices called %d times, want 1", relay.userDevicesCalls)
	}
	if len(devices) != 1 {
		t.Fatalf("GetUserDevices returned %d devices, want 1", len(devices))
	}

	if _, err := shadow.GetUserInfo(ctx, jids); err != nil {
		t.Fatalf("GetUserInfo returned error: %v", err)
	}
	if relay.userInfoCalls != 1 {
		t.Fatalf("relay.GetUserInfo called %d times, want 1", relay.userInfoCalls)
	}

	// Delegated decryption via DangerousInternals.
	child := &waBinary.Node{Tag: "enc", Content: []byte("ciphertext")}
	pt, _, err := shadow.DangerousInternals().DecryptDM(ctx, child, jids[0], false, time.Time{})
	if err != nil {
		t.Fatalf("DecryptDM returned error: %v", err)
	}
	if string(pt) != "decrypted-by-relay" {
		t.Fatalf("DecryptDM plaintext = %q, want %q", pt, "decrypted-by-relay")
	}
	if relay.decryptDMCalls != 1 {
		t.Fatalf("relay.DecryptDM called %d times, want 1", relay.decryptDMCalls)
	}
}

// TestShadowGenerateIDsWork verifies the socketless client can still generate
// message/request IDs (no transport needed).
func TestShadowGenerateIDsWork(t *testing.T) {
	shadow := newTestShadow(t, &fakeShadowRelay{})
	if shadow.GenerateMessageID() == "" {
		t.Fatal("GenerateMessageID returned empty string")
	}
	if shadow.DangerousInternals().GenerateRequestID() == "" {
		t.Fatal("GenerateRequestID returned empty string")
	}
}
