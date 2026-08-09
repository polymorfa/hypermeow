package whatsmeow

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow/proto/waFingerprint"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type identityReaderStore struct {
	keys map[string][32]byte
}

func (*identityReaderStore) PutIdentity(context.Context, string, [32]byte) error { return nil }
func (*identityReaderStore) DeleteAllIdentities(context.Context, string) error   { return nil }
func (*identityReaderStore) DeleteIdentity(context.Context, string) error        { return nil }
func (*identityReaderStore) IsTrustedIdentity(context.Context, string, [32]byte) (bool, error) {
	return true, nil
}
func (irs *identityReaderStore) GetManyIdentities(_ context.Context, addresses []string) (map[string][32]byte, error) {
	result := make(map[string][32]byte, len(addresses))
	for _, address := range addresses {
		if key, ok := irs.keys[address]; ok {
			result[address] = key
		}
	}
	return result, nil
}

var _ store.IdentityKeyReader = (*identityReaderStore)(nil)

func TestGenerateNumericSecurityCodeMatchesWhatsAppWebV4(t *testing.T) {
	localKeys := [][32]byte{
		*(*[32]byte)(bytes.Repeat([]byte{0x22}, 32)),
		*(*[32]byte)(bytes.Repeat([]byte{0x11}, 32)),
	}
	remoteKeys := [][32]byte{
		*(*[32]byte)(bytes.Repeat([]byte{0x33}, 32)),
	}

	got, err := generateNumericSecurityCode(
		context.Background(),
		[]byte("100000000000001"),
		localKeys,
		[]byte("100000000000002"),
		remoteKeys,
	)
	if err != nil {
		t.Fatal(err)
	}
	const want = "225825860855586870704874202827422423772749730831393050598207"
	if got != want {
		t.Fatalf("security code = %q, want %q", got, want)
	}
}

func TestGenerateNumericSecurityCodeHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := generateNumericSecurityCode(
		ctx,
		[]byte("100000000000001"),
		[][32]byte{{}},
		[]byte("100000000000002"),
		[][32]byte{{}},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestBuildIdentityVerificationQRCodesMatchesWhatsAppWebV3(t *testing.T) {
	local := identityVerificationFingerprint{
		LID:      types.NewJID("100000000000001", types.HiddenUserServer),
		Phone:    types.NewJID("15550000001", types.DefaultUserServer),
		Username: "local_user",
		Keys: [][32]byte{
			*(*[32]byte)(bytes.Repeat([]byte{0x22}, 32)),
			*(*[32]byte)(bytes.Repeat([]byte{0x11}, 32)),
		},
	}
	remote := identityVerificationFingerprint{
		LID:      types.NewJID("100000000000002", types.HiddenUserServer),
		Phone:    types.NewJID("15550000002", types.DefaultUserServer),
		Username: "remote_user",
		Keys: [][32]byte{
			*(*[32]byte)(bytes.Repeat([]byte{0x33}, 32)),
		},
	}

	displayBytes, verifyBytes, err := buildIdentityVerificationQRCodes(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	var display, verify waFingerprint.CombinedFingerprint
	if err = proto.Unmarshal(displayBytes, &display); err != nil {
		t.Fatal(err)
	}
	if err = proto.Unmarshal(verifyBytes, &verify); err != nil {
		t.Fatal(err)
	}
	if display.GetVersion() != 1 || verify.GetVersion() != 1 {
		t.Fatalf("unexpected versions: display=%d verify=%d", display.GetVersion(), verify.GetVersion())
	}
	assertFingerprintIdentifiers(t, display.GetLocalFingerprint(), local)
	assertFingerprintIdentifiers(t, display.GetRemoteFingerprint(), remote)
	if len(display.GetLocalFingerprint().GetPublicKey()) != 0 || len(display.GetRemoteFingerprint().GetPublicKey()) != 0 {
		t.Fatal("display QR exposed unhashed identity keys")
	}
	localSerialized := serializeIdentityKeys(local.Keys)
	remoteSerialized := serializeIdentityKeys(remote.Keys)
	if !bytes.Equal(verify.GetLocalFingerprint().GetPublicKey(), localSerialized) ||
		!bytes.Equal(verify.GetRemoteFingerprint().GetPublicKey(), remoteSerialized) {
		t.Fatal("verification QR did not contain the sorted identity key sets")
	}
	const localHash = "9448c8bd61d1029632a6d2bba3ed50a23cfb85fd6900cae6d7b7248514291e9b28b32a5f48e9bedb826d8fd64fc6ae004ca9abb68f0e893d0c8d927ac41598d5"
	const remoteHash = "02368e44bee980c294e96347298f06b584ca133fb617600b5004f6434330e3bb7c2339fd8d492d4541bd2f80bec1b8528518a58c896fdc7918d8cc599eecdc0a"
	if hex.EncodeToString(display.GetLocalFingerprint().GetHashedPublicKey()) != localHash ||
		hex.EncodeToString(display.GetRemoteFingerprint().GetHashedPublicKey()) != remoteHash {
		t.Fatal("display QR identity-key hashes do not match WhatsApp Web")
	}
}

func TestBuildIdentityVerificationQRCodesRejectsMissingKeys(t *testing.T) {
	_, _, err := buildIdentityVerificationQRCodes(
		identityVerificationFingerprint{LID: types.NewJID("100000000000001", types.HiddenUserServer)},
		identityVerificationFingerprint{LID: types.NewJID("100000000000002", types.HiddenUserServer)},
	)
	if err == nil {
		t.Fatal("expected missing identity keys to fail")
	}
}

func TestNewIdentityVerificationCodesUsesLIDAsUserID(t *testing.T) {
	local := identityVerificationFingerprint{
		LID:  types.NewJID("100000000000001", types.HiddenUserServer),
		Keys: [][32]byte{*(*[32]byte)(bytes.Repeat([]byte{0x11}, 32))},
	}
	remote := identityVerificationFingerprint{
		LID:      types.NewJID("100000000000002", types.HiddenUserServer),
		Phone:    types.NewJID("15550000002", types.DefaultUserServer),
		Username: "remote_user",
		Keys:     [][32]byte{*(*[32]byte)(bytes.Repeat([]byte{0x22}, 32))},
	}

	got, err := newIdentityVerificationCodes(context.Background(), local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != remote.LID || got.PhoneNumber != remote.Phone || got.Username != remote.Username {
		t.Fatalf("unexpected identity aliases: %#v", got)
	}
	if len(got.NumericCode) != 60 || len(got.DisplayQRCode) == 0 || len(got.VerificationQRCode) == 0 {
		t.Fatalf("incomplete security-code result: %#v", got)
	}
}

func TestGetIdentityVerificationCodesRequiresLID(t *testing.T) {
	client := &Client{Store: &store.Device{}}
	_, err := client.GetIdentityVerificationCodes(
		context.Background(),
		types.NewJID("15550000002", types.DefaultUserServer),
	)
	if !errors.Is(err, ErrIdentityVerificationRequiresLID) {
		t.Fatalf("error = %v, want ErrIdentityVerificationRequiresLID", err)
	}
}

func TestSplitIdentityVerificationDevicesExcludesCurrentDevice(t *testing.T) {
	local := types.NewJID("100000000000001", types.HiddenUserServer)
	remote := types.NewJID("100000000000002", types.HiddenUserServer)
	devices := []types.JID{
		types.NewADJID(local.User, types.LIDDomain, 67),
		types.NewADJID(local.User, types.LIDDomain, 0),
		types.NewADJID(remote.User, types.LIDDomain, 0),
	}

	localDevices, remoteDevices := splitIdentityVerificationDevices(devices, local, remote, 67, true)
	if len(localDevices) != 1 || localDevices[0].Device != 0 {
		t.Fatalf("local devices = %v, want only device 0", localDevices)
	}
	if len(remoteDevices) != 1 || remoteDevices[0].Device != 0 {
		t.Fatalf("remote devices = %v, want only device 0", remoteDevices)
	}
}

func TestSplitIdentityVerificationDevicesKeepsDeviceZeroWithoutCurrentDevice(t *testing.T) {
	local := types.NewJID("100000000000001", types.HiddenUserServer)
	remote := types.NewJID("100000000000002", types.HiddenUserServer)
	devices := []types.JID{
		types.NewADJID(local.User, types.LIDDomain, 0),
		types.NewADJID(remote.User, types.LIDDomain, 0),
	}

	localDevices, _ := splitIdentityVerificationDevices(devices, local, remote, 0, false)
	if len(localDevices) != 1 || localDevices[0].Device != 0 {
		t.Fatalf("local devices = %v, want device 0", localDevices)
	}
}

func TestReadIdentityKeysUsesOptionalBatchReader(t *testing.T) {
	devices := []types.JID{
		types.NewADJID("100000000000001", types.LIDDomain, 1),
		types.NewADJID("100000000000001", types.LIDDomain, 2),
	}
	want := [][32]byte{
		*(*[32]byte)(bytes.Repeat([]byte{0x11}, 32)),
		*(*[32]byte)(bytes.Repeat([]byte{0x22}, 32)),
	}
	identityStore := &identityReaderStore{keys: map[string][32]byte{
		devices[0].SignalAddress().String(): want[0],
		devices[1].SignalAddress().String(): want[1],
	}}
	client := &Client{Store: &store.Device{Identities: identityStore}}

	got, err := client.readIdentityKeys(context.Background(), devices)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("identity keys = %#v, want %#v", got, want)
	}
}

func TestReadIdentityKeysRequiresOptionalBatchReader(t *testing.T) {
	client := &Client{Store: &store.Device{Identities: &identityStoreWithoutReader{}}}
	_, err := client.readIdentityKeys(context.Background(), []types.JID{
		types.NewADJID("100000000000001", types.LIDDomain, 1),
	})
	if !errors.Is(err, ErrIdentityKeyReaderUnsupported) {
		t.Fatalf("error = %v, want ErrIdentityKeyReaderUnsupported", err)
	}
}

type identityStoreWithoutReader struct{}

func (*identityStoreWithoutReader) PutIdentity(context.Context, string, [32]byte) error { return nil }
func (*identityStoreWithoutReader) DeleteAllIdentities(context.Context, string) error   { return nil }
func (*identityStoreWithoutReader) DeleteIdentity(context.Context, string) error        { return nil }
func (*identityStoreWithoutReader) IsTrustedIdentity(context.Context, string, [32]byte) (bool, error) {
	return true, nil
}

func assertFingerprintIdentifiers(t *testing.T, got *waFingerprint.FingerprintData, want identityVerificationFingerprint) {
	t.Helper()
	if got == nil {
		t.Fatal("missing fingerprint")
	}
	if string(got.GetLidIdentifier()) != want.LID.String() {
		t.Fatalf("LID identifier = %q, want %q", got.GetLidIdentifier(), want.LID.String())
	}
	if string(got.GetPnIdentifier()) != want.Phone.User {
		t.Fatalf("phone identifier = %q, want %q", got.GetPnIdentifier(), want.Phone.User)
	}
	if string(got.GetUsernameIdentifier()) != want.Username {
		t.Fatalf("username identifier = %q, want %q", got.GetUsernameIdentifier(), want.Username)
	}
}
