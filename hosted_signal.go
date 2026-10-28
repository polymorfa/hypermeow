// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
)

// HostedDeviceID is the device ID of a hosted (Cloud API coexistence) device
// on the hosted and hosted.lid servers (WAWebWid:20, WAWebBizCoexUtils).
const HostedDeviceID = 99

func isHostedJID(jid types.JID) bool {
	return jid.Server == types.HostedServer || jid.Server == types.HostedLIDServer
}

// rawHostedSender returns the hosted device that sent an incoming stanza, read
// from the raw participant or from attribute. parseMessageSource normalizes
// hosted senders to the regular servers for the public MessageInfo, which
// loses the distinction the Signal address needs.
func rawHostedSender(node *waBinary.Node) (types.JID, bool) {
	ag := node.AttrGetter()
	jid := ag.OptionalJIDOrEmpty("participant")
	if jid.IsEmpty() {
		jid = ag.OptionalJIDOrEmpty("from")
	}
	if !isHostedJID(jid) {
		return types.EmptyJID, false
	}
	return jid, true
}

// hostedSignalJID returns the Signal identity of a hosted device. Like
// WAWebSignalAddress:14-33 it is <lid>:99@hosted.lid when the account's LID is
// known and <pn>:99@hosted otherwise, so its sessions never share an address
// with a regular device 99 (<lid>:99@lid or <pn>:99@s.whatsapp.net). Sessions
// already stored under the PN hosted address move to the LID hosted address.
func (cli *Client) hostedSignalJID(ctx context.Context, jid types.JID) types.JID {
	if jid.Device != HostedDeviceID {
		cli.Log.Warnf("Hosted JID %s does not use device %d", jid, HostedDeviceID)
	}
	if jid.Server != types.HostedServer {
		return jid
	}
	lid, err := cli.Store.LIDs.GetLIDForPN(ctx, types.JID{User: jid.User, Server: types.DefaultUserServer})
	if err != nil {
		cli.Log.Warnf("Failed to get LID for hosted device %s: %v", jid, err)
		return jid
	} else if lid.IsEmpty() {
		return jid
	}
	hostedLID := types.JID{User: lid.User, Device: jid.Device, Server: types.HostedLIDServer}
	cli.migrateSessionStore(ctx, jid, hostedLID)
	return hostedLID
}
