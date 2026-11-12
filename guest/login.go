// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package guest

import (
	"errors"
	"strconv"

	"google.golang.org/protobuf/proto"

	whatsmeow "github.com/polymorfa/hypermeow"
	"github.com/polymorfa/hypermeow/proto/waWa6"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

// Apply records the registered identity on the device: its ID and LID become
// the guest LID (device 0) and its push name the guest's display name.
func Apply(device *store.Device, reg *Registration) error {
	if device == nil || reg == nil || reg.LID.Server != types.HiddenUserServer || reg.LID.Device != 0 {
		return errors.New("guest: device and registration are required")
	}
	if value, err := strconv.ParseUint(reg.LID.User, 10, 64); err != nil || value == 0 {
		return errors.New("guest: registration has no valid guest identity")
	}
	if device.ID != nil && *device.ID != reg.LID {
		return errors.New("guest: device already belongs to another identity")
	}
	id := types.JID{User: reg.LID.User, Server: types.HiddenUserServer}
	device.ID = &id
	device.LID = id
	device.PushName = reg.Name
	return nil
}

// LoginPayload builds the guest login ClientPayload for a device that Apply
// has prepared.
//
// It matches the WhatsApp Web guest bootstrap override (username = LID,
// device 0, product = WHATSAPP_LID, accountType = GUEST, pushName;
// WAWebGuestCoreBackendBootstrap.js:7-14 in public guest build 1053925913)
// and the active login it is merged into (passive false, pull true;
// WAWebGuestCoreBackendConsts.js:22-24, WAWebGetClientPayloadForLogin.js:14-17).
func LoginPayload(device *store.Device) (*waWa6.ClientPayload, error) {
	if device == nil || device.ID == nil || device.ID.Server != types.HiddenUserServer || device.ID.Device != 0 {
		return nil, errors.New("guest: device has no guest identity; call Apply first")
	}
	if value, err := strconv.ParseUint(device.ID.User, 10, 64); err != nil || value == 0 {
		return nil, errors.New("guest: device has no valid guest identity")
	}
	payload := proto.Clone(store.BaseClientPayload).(*waWa6.ClientPayload)
	payload.Username = proto.Uint64(device.ID.UserInt())
	payload.Device = proto.Uint32(uint32(device.ID.Device))
	payload.Product = waWa6.ClientPayload_WHATSAPP_LID.Enum()
	payload.AccountType = waWa6.ClientPayload_GUEST.Enum()
	payload.Passive = proto.Bool(false)
	payload.Pull = proto.Bool(true)
	payload.LidDbMigrated = proto.Bool(true)
	payload.Lc = proto.Int32(1)
	if device.PushName != "" {
		payload.PushName = proto.String(device.PushName)
	}
	return payload, nil
}

// NewClient returns a client that logs in as the registered guest. It applies
// reg to device first, so the client starts out authenticated, then sends the
// guest login payload, skips the post-connect passive IQ (the payload already
// says passive false, as WhatsApp Web does) and uses the registered browser's
// User-Agent for the WebSocket. Use a device from NewDevice.
func NewClient(device *store.Device, reg *Registration, log waLog.Logger) (*whatsmeow.Client, error) {
	if device == nil || !device.Initialized || device.PreKeys == nil {
		return nil, errors.New("guest: device has no stores; use guest.NewDevice")
	}
	if err := Apply(device, reg); err != nil {
		return nil, err
	}
	if _, err := LoginPayload(device); err != nil {
		return nil, err
	}
	cli := whatsmeow.NewClient(device, log)
	cli.GetClientPayload = func() *waWa6.ClientPayload {
		payload, _ := LoginPayload(device)
		return payload
	}
	cli.DisablePostConnectPassiveIQ = true
	if reg.Browser.UserAgent != "" {
		cli.UserAgent = reg.Browser.UserAgent
	}
	return cli, nil
}
