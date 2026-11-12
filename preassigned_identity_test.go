// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	"github.com/polymorfa/hypermeow/util/keys"
)

// A device whose identity is assigned before NewClient, with working stores
// and no ADV account (the guest package's shape), must authenticate on
// connect success and dispatch Connected without touching a missing store.
func TestConnectSuccessForPreassignedIdentity(t *testing.T) {
	lid := types.NewJID("123456789", types.HiddenUserServer)
	device := &store.Device{
		NoiseKey: keys.NewKeyPair(), IdentityKey: keys.NewKeyPair(), RegistrationID: 1,
		ID: &lid, LID: lid,
	}
	device.SignedPreKey = device.IdentityKey.CreateSignedPreKey(1)
	noop := &store.NoopStore{}
	device.SetAllStores(noop)
	device.LIDs = noop
	device.Container = noop
	device.Initialized = true

	cli := NewClient(device, nil)
	cli.DisablePostConnectPassiveIQ = true
	connected := make(chan struct{}, 1)
	cli.AddEventHandler(func(evt any) {
		if _, ok := evt.(*events.Connected); ok {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	})
	cli.handleConnectSuccess(context.Background(), &waBinary.Node{
		Tag: "success", Attrs: waBinary.Attrs{"lid": lid, "t": "1700000000"},
	})
	if !cli.IsLoggedIn() {
		t.Fatal("client with a preassigned identity ignored connect success")
	}
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("Connected was not dispatched")
	}
}
