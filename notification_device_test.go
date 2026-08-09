package whatsmeow

import (
	"context"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestDeviceNotificationUpdatesLIDOnlyCache(t *testing.T) {
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)
	existingLID := types.NewADJID(lid.User, 0, 1)
	addedLID := types.NewADJID(lid.User, 0, 2)
	addedPN := types.NewADJID(pn.User, 0, 2)
	wantDevices := []types.JID{existingLID, addedLID}
	cli := &Client{
		Store: store.NoopDevice,
		Log:   waLog.Noop,
		userDevicesCache: map[types.JID]deviceCache{
			lid: {devices: []types.JID{existingLID}, dhash: participantListHashV2([]types.JID{existingLID})},
		},
	}

	cli.handleDeviceNotification(context.Background(), &waBinary.Node{
		Tag:   "notification",
		Attrs: waBinary.Attrs{"from": pn, "lid": lid},
		Content: []waBinary.Node{{
			Tag: "add",
			Attrs: waBinary.Attrs{
				"device_hash":     "unused",
				"device_lid_hash": participantListHashV2(wantDevices),
			},
			Content: []waBinary.Node{{Tag: "device", Attrs: waBinary.Attrs{"jid": addedPN, "lid": addedLID}}},
		}},
	})

	got := cli.userDevicesCache[lid].devices
	if len(got) != 2 || got[0] != existingLID || got[1] != addedLID {
		t.Fatalf("LID cache was not updated: %#v", got)
	}
}
