package whatsmeow

import (
	"context"
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
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

func TestDeviceNotificationInvalidatesLIDCacheWithoutCompleteMetadata(t *testing.T) {
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)
	existingLID := types.NewADJID(lid.User, 0, 1)
	addedLID := types.NewADJID(lid.User, 0, 2)
	addedPN := types.NewADJID(pn.User, 0, 2)

	tests := []struct {
		name       string
		childAttrs waBinary.Attrs
		attrs      waBinary.Attrs
	}{
		{
			name:       "missing device LID",
			attrs:      waBinary.Attrs{"device_hash": "unused", "device_lid_hash": "unused"},
			childAttrs: waBinary.Attrs{"jid": addedPN},
		},
		{
			name:       "missing LID hash",
			attrs:      waBinary.Attrs{"device_hash": "unused"},
			childAttrs: waBinary.Attrs{"jid": addedPN, "lid": addedLID},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
					Tag:     "add",
					Attrs:   test.attrs,
					Content: []waBinary.Node{{Tag: "device", Attrs: test.childAttrs}},
				}},
			})
			if _, ok := cli.userDevicesCache[lid]; ok {
				t.Fatal("incomplete notification retained the LID device cache")
			}
		})
	}
}
