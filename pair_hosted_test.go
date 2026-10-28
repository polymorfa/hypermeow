// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"testing"

	"github.com/polymorfa/libsignal-protocol-go/ecc"
	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/proto/waAdv"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
	"github.com/polymorfa/hypermeow/util/keys"
)

type hostedPairContainer struct {
	store.NoopStore
	saved    int
	isHosted bool
}

func (c *hostedPairContainer) PutDevice(_ context.Context, device *store.Device) error {
	c.saved++
	c.isHosted = device.IsHosted
	return nil
}

// buildPairDeviceIdentity builds the <device-identity> content of a pair-success
// stanza the way the primary does: an ADVSignedDeviceIdentityHMAC whose HMAC
// covers [6,5] || details for HOSTED accounts (WAWebHandlePairSuccess:89-93).
func buildPairDeviceIdentity(t *testing.T, device *store.Device, accountType waAdv.ADVEncryptionType) []byte {
	t.Helper()
	primary := keys.NewKeyPair()
	details, err := proto.Marshal(&waAdv.ADVDeviceIdentity{
		RawID:       proto.Uint32(1),
		Timestamp:   proto.Uint64(1700000000),
		KeyIndex:    proto.Uint32(1),
		AccountType: accountType.Enum(),
		DeviceType:  waAdv.ADVEncryptionType_E2EE.Enum(),
	})
	if err != nil {
		t.Fatal(err)
	}
	accountSig := ecc.CalculateSignature(ecc.NewDjbECPrivateKey(*primary.Priv), concatBytes(AdvAccountSignaturePrefix, details, device.IdentityKey.Pub[:]))
	signed, err := proto.Marshal(&waAdv.ADVSignedDeviceIdentity{
		Details:             details,
		AccountSignatureKey: primary.Pub[:],
		AccountSignature:    accountSig[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, device.AdvSecretKey)
	if accountType == waAdv.ADVEncryptionType_HOSTED {
		mac.Write(AdvHostedAccountSignaturePrefix)
	}
	mac.Write(signed)
	container, err := proto.Marshal(&waAdv.ADVSignedDeviceIdentityHMAC{
		Details:     signed,
		HMAC:        mac.Sum(nil),
		AccountType: accountType.Enum(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return container
}

func TestHandlePairPersistsHostedAccount(t *testing.T) {
	for _, tc := range []struct {
		name        string
		accountType waAdv.ADVEncryptionType
		want        bool
	}{
		{"hosted", waAdv.ADVEncryptionType_HOSTED, true},
		{"e2ee", waAdv.ADVEncryptionType_E2EE, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			container := &hostedPairContainer{}
			device := &store.Device{
				NoiseKey:     keys.NewKeyPair(),
				IdentityKey:  keys.NewKeyPair(),
				AdvSecretKey: make([]byte, 32),
				Container:    container,
				LIDs:         &store.NoopStore{},
			}
			device.SetAllStores(&store.NoopStore{})
			cli := NewClient(device, waLog.Noop)
			jid := types.NewJID("15551234567", types.DefaultUserServer)
			jid.Device = 12
			lid := types.NewJID("123456789012345", types.HiddenUserServer)
			lid.Device = 12

			// The client is offline, so the final pair-device-sign send fails after the store is saved.
			_ = cli.handlePair(context.Background(), buildPairDeviceIdentity(t, device, tc.accountType), "pair-1", "", "smba", jid, lid)
			if container.saved == 0 {
				t.Fatal("device was not saved")
			}
			if container.isHosted != tc.want {
				t.Fatalf("saved IsHosted = %v, want %v", container.isHosted, tc.want)
			}
		})
	}
}
