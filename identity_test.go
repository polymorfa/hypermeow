package whatsmeow

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/appstate"
	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/proto/waFingerprint"
	"github.com/polymorfa/hypermeow/proto/waHistorySync"
	"github.com/polymorfa/hypermeow/proto/waSyncAction"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

func TestParseGroupParticipantPreservesUsername(t *testing.T) {
	node := &waBinary.Node{Tag: "participant", Attrs: waBinary.Attrs{
		"jid":      types.NewJID("100000011111111", types.HiddenUserServer),
		"username": "example",
	}}
	ag := node.AttrGetter()
	participant := parseParticipant(ag, node)
	if participant.Username != "example" {
		t.Fatalf("username = %q", participant.Username)
	}
}

type cachedLIDStore struct {
	store.NoopStore
	pn  types.JID
	lid types.JID
}

func (cached *cachedLIDStore) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	if pn.ToNonAD() == cached.pn {
		return cached.lid, nil
	}
	return types.EmptyJID, nil
}

func TestResolveLIDUsesCachedMapping(t *testing.T) {
	pn := types.NewADJID("15550001111", types.WhatsAppDomain, 7)
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	lids := &cachedLIDStore{pn: pn.ToNonAD(), lid: lid}
	client := NewClient(&store.Device{LIDs: lids}, waLog.Noop)

	resolved, err := client.ResolveLID(context.Background(), pn)
	if err != nil {
		t.Fatal(err)
	}
	want := lid
	want.Device = pn.Device
	if resolved != want {
		t.Fatalf("resolved LID = %s, want %s", resolved, want)
	}
}

func TestResolveLIDRejectsNonPhoneJID(t *testing.T) {
	client := NewClient(&store.Device{LIDs: &store.NoopStore{}}, waLog.Noop)
	if _, err := client.ResolveLID(context.Background(), types.NewJID("100000011111111", types.HiddenUserServer)); err == nil {
		t.Fatal("expected non-PN JID to fail")
	}
}

type blockingMessageNameStore struct {
	store.NoopStore
	entered chan struct{}
	release chan struct{}
}

func TestMessageNameUpdatesRemainAsyncByDefault(t *testing.T) {
	contacts := &blockingMessageNameStore{entered: make(chan struct{}), release: make(chan struct{})}
	client := &Client{Store: &store.Device{Contacts: contacts}}
	returned := make(chan struct{})
	go func() {
		client.updateMessageContactNames(context.Background(), &types.MessageInfo{
			MessageSource: types.MessageSource{Sender: types.NewJID("15550001111", types.DefaultUserServer)},
			PushName:      "Benchmark Sender",
		})
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("default message name update blocked on the store write")
	}
	<-contacts.entered
	close(contacts.release)
}

func (s *blockingMessageNameStore) PutPushName(context.Context, types.JID, string) (bool, string, error) {
	close(s.entered)
	<-s.release
	return false, "", nil
}

func TestSynchronousMessageNameUpdatesWaitForStore(t *testing.T) {
	contacts := &blockingMessageNameStore{entered: make(chan struct{}), release: make(chan struct{})}
	client := &Client{Store: &store.Device{Contacts: contacts}}
	client.setSynchronousMessageNameUpdates(true)
	done := make(chan struct{})
	go func() {
		client.updateMessageContactNames(context.Background(), &types.MessageInfo{
			MessageSource: types.MessageSource{Sender: types.NewJID("15550001111", types.DefaultUserServer)},
			PushName:      "Benchmark Sender",
		})
		close(done)
	}()

	<-contacts.entered
	select {
	case <-done:
		t.Fatal("synchronous message name update returned before the store write")
	default:
	}
	close(contacts.release)
	<-done
}

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

type identityChangeStore struct {
	store.NoopStore
	lid             types.JID
	pn              types.JID
	identityDeletes []string
	sessionDeletes  []string
}

func (s *identityChangeStore) DeleteAllIdentities(_ context.Context, user string) error {
	s.identityDeletes = append(s.identityDeletes, user)
	return nil
}

func (s *identityChangeStore) DeleteAllSessions(_ context.Context, user string) error {
	s.sessionDeletes = append(s.sessionDeletes, user)
	return nil
}

func (s *identityChangeStore) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	if pn.User == s.pn.User {
		return s.lid, nil
	}
	return types.EmptyJID, nil
}

func (s *identityChangeStore) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	if lid.User == s.lid.User {
		return s.pn, nil
	}
	return types.EmptyJID, nil
}

func TestIdentityChangeDeletesPNAndLIDSignalState(t *testing.T) {
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)
	recorder := &identityChangeStore{pn: pn, lid: lid}
	client := NewClient(&store.Device{
		Identities:    recorder,
		Sessions:      recorder,
		LIDs:          recorder,
		PrivacyTokens: recorder,
	}, waLog.Noop)

	client.handleEncryptNotification(context.Background(), &waBinary.Node{
		Tag:     "notification",
		Attrs:   waBinary.Attrs{"from": pn},
		Content: []waBinary.Node{{Tag: "identity"}},
	})

	want := []string{pn.User, pn.User + "_128", lid.User + "_1", lid.User + "_129"}
	if !slices.Equal(recorder.identityDeletes, want) {
		t.Fatalf("identity deletes = %v, want %v", recorder.identityDeletes, want)
	}
	if !slices.Equal(recorder.sessionDeletes, want) {
		t.Fatalf("session deletes = %v, want %v", recorder.sessionDeletes, want)
	}
}

func TestIdentityChangeFromLIDDeletesMappedPNState(t *testing.T) {
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)
	recorder := &identityChangeStore{pn: pn, lid: lid}
	client := NewClient(&store.Device{
		Identities:    recorder,
		Sessions:      recorder,
		LIDs:          recorder,
		PrivacyTokens: recorder,
	}, waLog.Noop)

	client.handleEncryptNotification(context.Background(), &waBinary.Node{
		Tag:     "notification",
		Attrs:   waBinary.Attrs{"from": lid},
		Content: []waBinary.Node{{Tag: "identity"}},
	})

	want := []string{lid.User + "_1", lid.User + "_129", pn.User, pn.User + "_128"}
	if !slices.Equal(recorder.identityDeletes, want) {
		t.Fatalf("identity deletes = %v, want %v", recorder.identityDeletes, want)
	}
	if !slices.Equal(recorder.sessionDeletes, want) {
		t.Fatalf("session deletes = %v, want %v", recorder.sessionDeletes, want)
	}
}

func TestBuildRequestPhoneNumberMessage(t *testing.T) {
	contextInfo := &waE2E.ContextInfo{StanzaID: stringPtr("request-id")}
	message := BuildRequestPhoneNumberMessage(contextInfo)

	request := message.GetRequestPhoneNumberMessage()
	if request == nil {
		t.Fatal("expected request phone number message")
	}
	if request.GetContextInfo().GetStanzaID() != "request-id" {
		t.Fatalf("unexpected context info: %+v", request.GetContextInfo())
	}
}

func TestBuildSharePhoneNumberMessage(t *testing.T) {
	message := BuildSharePhoneNumberMessage()
	protocolMessage := message.GetProtocolMessage()
	if protocolMessage == nil {
		t.Fatal("expected protocol message")
	}
	if protocolMessage.GetType() != waE2E.ProtocolMessage_SHARE_PHONE_NUMBER {
		t.Fatalf("unexpected protocol message type: %s", protocolMessage.GetType())
	}
}

func stringPtr(value string) *string {
	return &value
}

type identityReaderStore struct {
	lock       sync.Mutex
	keys       map[string][32]byte
	includeAll bool
	generation uint64
}

func (*identityReaderStore) PutIdentity(context.Context, string, [32]byte) error { return nil }
func (*identityReaderStore) DeleteAllIdentities(context.Context, string) error   { return nil }
func (*identityReaderStore) DeleteIdentity(context.Context, string) error        { return nil }
func (*identityReaderStore) IsTrustedIdentity(context.Context, string, [32]byte) (bool, error) {
	return true, nil
}
func (irs *identityReaderStore) GetManyIdentities(_ context.Context, addresses []string) (map[string][32]byte, uint64, error) {
	irs.lock.Lock()
	defer irs.lock.Unlock()
	result := make(map[string][32]byte, len(addresses))
	if irs.includeAll {
		for address, key := range irs.keys {
			result[address] = key
		}
		return result, irs.generation, nil
	}
	for _, address := range addresses {
		if key, ok := irs.keys[address]; ok {
			result[address] = key
		}
	}
	return result, irs.generation, nil
}
func (irs *identityReaderStore) EnsureIdentity(_ context.Context, address string, key [32]byte, deleteGeneration uint64) (bool, error) {
	irs.lock.Lock()
	defer irs.lock.Unlock()
	if deleteGeneration != irs.generation {
		return false, nil
	}
	if existing, ok := irs.keys[address]; ok {
		return existing == key, nil
	}
	if irs.keys == nil {
		irs.keys = make(map[string][32]byte)
	}
	irs.keys[address] = key
	return true, nil
}

func TestReadIdentityKeysIgnoresUnrequestedReaderEntries(t *testing.T) {
	device := types.NewADJID("100000000000001", types.LIDDomain, 1)
	want := [32]byte{1}
	identities := &identityReaderStore{includeAll: true, keys: map[string][32]byte{
		device.SignalAddress().String(): want,
		"unrequested:1":                 {2},
	}}
	client := &Client{Store: &store.Device{Identities: identities}}
	keys, err := client.readIdentityKeys(context.Background(), []types.JID{device})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != want {
		t.Fatalf("identity keys = %x, want %x", keys, want)
	}
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

func TestIdentityVerificationFingerprintMarksHostedDevices(t *testing.T) {
	devices := []types.JID{
		types.NewADJID("100000000000002", types.LIDDomain, 1),
		types.NewADJID("100000000000002", types.HostedLIDDomain, 2),
	}
	if !hasHostedIdentityDevice(devices) {
		t.Fatal("hosted identity device was labeled E2EE")
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

func TestFilterContactsPreservesUsername(t *testing.T) {
	client := &Client{}
	_, contacts := client.filterContacts([]appstate.Mutation{
		{
			Index: []string{appstate.IndexContact, "100000011111111@lid"},
			Action: &waSyncAction.SyncActionValue{ContactAction: &waSyncAction.ContactAction{
				FullName: proto.String("Example User"),
				Username: proto.String("example"),
			}},
		},
		{
			Index: []string{appstate.IndexLIDContact, "100000022222222@lid"},
			Action: &waSyncAction.SyncActionValue{LidContactAction: &waSyncAction.LidContactAction{
				FullName: proto.String("LID User"),
				Username: proto.String("lid-example"),
			}},
		},
	})
	if len(contacts) != 2 {
		t.Fatalf("got %d contacts", len(contacts))
	}
	if contacts[0].Username != "example" || contacts[1].Username != "lid-example" {
		t.Fatalf("usernames = %q, %q", contacts[0].Username, contacts[1].Username)
	}
	if !contacts[0].UsernameSet || !contacts[1].UsernameSet {
		t.Fatal("snapshot usernames were not marked authoritative")
	}
}

type recordingLIDContactStore struct {
	store.NoopStore
	jid      types.JID
	fullName string
	username string
}

func (contacts *recordingLIDContactStore) PutContactName(_ context.Context, jid types.JID, _, fullName string) error {
	contacts.jid = jid
	contacts.fullName = fullName
	return nil
}

func (contacts *recordingLIDContactStore) PutContactUsername(_ context.Context, jid types.JID, username string) error {
	contacts.jid = jid
	contacts.username = username
	return nil
}

func TestDispatchLIDContactPersistsNamesAndUsername(t *testing.T) {
	contacts := &recordingLIDContactStore{}
	client := &Client{Store: &store.Device{Contacts: contacts}}
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	event := client.dispatchAppState(context.Background(), appstate.WAPatchCriticalUnblockLow, appstate.Mutation{
		Index: []string{appstate.IndexLIDContact, lid.String()},
		Action: &waSyncAction.SyncActionValue{LidContactAction: &waSyncAction.LidContactAction{
			FullName: proto.String("LID User"), Username: proto.String("lid-example"),
		}},
	}, false)
	if contacts.jid != lid || contacts.fullName != "LID User" || contacts.username != "lid-example" {
		t.Fatalf("unexpected persisted contact: %#v", contacts)
	}
	lidEvent, ok := event.(*events.LIDContact)
	if !ok || lidEvent.JID != lid || lidEvent.Action.GetUsername() != "lid-example" {
		t.Fatalf("unexpected LID contact event: %#v", event)
	}
}

type singleUsernameStore struct {
	store.NoopStore
	entries []store.ContactUsernameEntry
}

type failingUsernameStore struct {
	store.NoopStore
	called bool
}

type partiallyFailingUsernameStore struct {
	store.NoopStore
	entries []store.ContactUsernameEntry
}

func (partial *partiallyFailingUsernameStore) PutContactUsername(_ context.Context, user types.JID, username string) error {
	if username == "first" {
		return errors.New("synthetic first-write failure")
	}
	partial.entries = append(partial.entries, store.ContactUsernameEntry{JID: user, Username: username})
	return nil
}

func (failing *failingUsernameStore) PutContactUsername(context.Context, types.JID, string) error {
	failing.called = true
	return errors.New("synthetic username cache failure")
}

func (single *singleUsernameStore) PutContactUsername(_ context.Context, user types.JID, username string) error {
	single.entries = append(single.entries, store.ContactUsernameEntry{JID: user, Username: username})
	return nil
}

func TestContactUsernameStoreRemainsSingleWriteCompatible(t *testing.T) {
	var contacts store.ContactStore = &singleUsernameStore{}
	if _, ok := contacts.(store.ContactUsernameStore); !ok {
		t.Fatal("single-write username store no longer satisfies ContactUsernameStore")
	}
}

func TestPutContactUsernamesFallsBackToSingleWrites(t *testing.T) {
	contacts := &singleUsernameStore{}
	entries := []store.ContactUsernameEntry{
		{JID: types.NewJID("100000011111111", types.HiddenUserServer), Username: "first"},
		{JID: types.NewJID("100000022222222", types.HiddenUserServer), Username: "second"},
	}
	if err := putContactUsernames(context.Background(), contacts, entries); err != nil {
		t.Fatal(err)
	}
	if len(contacts.entries) != len(entries) {
		t.Fatalf("stored %d usernames, want %d", len(contacts.entries), len(entries))
	}
	for index := range entries {
		if contacts.entries[index] != entries[index] {
			t.Fatalf("stored entry %d = %#v, want %#v", index, contacts.entries[index], entries[index])
		}
	}
}

func TestPutContactUsernamesContinuesAfterSingleWriteFailure(t *testing.T) {
	contacts := &partiallyFailingUsernameStore{}
	second := store.ContactUsernameEntry{JID: types.NewJID("100000022222222", types.HiddenUserServer), Username: "second"}
	err := putContactUsernames(context.Background(), contacts, []store.ContactUsernameEntry{
		{JID: types.NewJID("100000011111111", types.HiddenUserServer), Username: "first"},
		second,
	})
	if err == nil {
		t.Fatal("single-write failure was not returned")
	}
	if len(contacts.entries) != 1 || contacts.entries[0] != second {
		t.Fatalf("writes after the first failure were skipped: %#v", contacts.entries)
	}
}

func TestContactUsernamePersistenceIsBestEffort(t *testing.T) {
	contacts := &failingUsernameStore{}
	client := &Client{Store: &store.Device{Contacts: contacts}, Log: waLog.Noop}
	client.storeContactUsernamesBestEffort(context.Background(), []store.ContactUsernameEntry{{
		JID: types.NewJID("100000011111111", types.HiddenUserServer), Username: "example",
	}})
	if !contacts.called {
		t.Fatal("username cache write was not attempted")
	}
}

func TestGroupContactUsernamesUseStableLIDs(t *testing.T) {
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	entries := groupContactUsernames(&types.GroupInfo{Participants: []types.GroupParticipant{
		{JID: lid, LID: lid, Username: "example"},
		{JID: types.NewJID("15550001111", types.DefaultUserServer), Username: "missing-lid"},
	}})
	if len(entries) != 1 || entries[0].JID != lid || entries[0].Username != "example" {
		t.Fatalf("unexpected group username entries: %#v", entries)
	}
}

func TestGroupParticipantUsernamesUseStableLIDs(t *testing.T) {
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	entries := groupParticipantUsernames([]types.GroupParticipant{{
		JID: types.NewJID("15550001111", types.DefaultUserServer), LID: lid, Username: "example",
	}})
	if len(entries) != 1 || entries[0].JID != lid || entries[0].Username != "example" {
		t.Fatalf("unexpected participant username entries: %#v", entries)
	}
}

func TestParseGroupResponsePersistsParticipantUsernames(t *testing.T) {
	contacts := &singleUsernameStore{}
	client := &Client{Store: &store.Device{Contacts: contacts}, Log: waLog.Noop}
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	groupNode := &waBinary.Node{
		Tag:   "group",
		Attrs: waBinary.Attrs{"id": "120363000000000000"},
		Content: []waBinary.Node{{
			Tag:   "participant",
			Attrs: waBinary.Attrs{"jid": lid, "username": "example"},
		}},
	}
	info, err := client.parseGroupNodeAndStoreUsernames(context.Background(), groupNode)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Participants) != 1 || len(contacts.entries) != 1 {
		t.Fatalf("parsed participants = %d, stored usernames = %#v", len(info.Participants), contacts.entries)
	}
	if contacts.entries[0].JID != lid || contacts.entries[0].Username != "example" {
		t.Fatalf("stored username = %#v", contacts.entries[0])
	}
}

func TestParseGroupChangeReturnsParticipantUsernames(t *testing.T) {
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	node := &waBinary.Node{
		Tag:   "notification",
		Attrs: waBinary.Attrs{"from": types.NewJID("120363000000000000", types.GroupServer), "t": "1"},
		Content: []waBinary.Node{{
			Tag: "add",
			Content: []waBinary.Node{{
				Tag:   "participant",
				Attrs: waBinary.Attrs{"jid": lid, "username": "example"},
			}},
		}},
	}
	_, _, usernames, err := (&Client{}).parseGroupChangeWithUsernames(node)
	if err != nil {
		t.Fatal(err)
	}
	if len(usernames) != 1 || usernames[0].JID != lid || usernames[0].Username != "example" {
		t.Fatalf("group-change usernames = %#v", usernames)
	}
}

func TestParseGroupParticipantRequestsReturnsUsernamesByStableLID(t *testing.T) {
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	pn := types.NewJID("15550001111", types.DefaultUserServer)
	nodes := []waBinary.Node{
		{Tag: "membership_approval_request", Attrs: waBinary.Attrs{
			"jid": lid, "username": "lid-user", "request_time": "1",
		}},
		{Tag: "membership_approval_request", Attrs: waBinary.Attrs{
			"jid": pn, "lid": lid, "username": "pn-user", "request_time": "2",
		}},
	}

	requests, usernames := parseGroupParticipantRequests(nodes)
	if len(requests) != 2 || requests[0].JID != lid || requests[1].JID != pn {
		t.Fatalf("participant requests = %#v", requests)
	}
	if len(usernames) != 2 {
		t.Fatalf("usernames = %#v", usernames)
	}
	if usernames[0].JID != lid || usernames[0].Username != "lid-user" {
		t.Fatalf("LID-addressed username = %#v", usernames[0])
	}
	if usernames[1].JID != lid || usernames[1].Username != "pn-user" {
		t.Fatalf("PN-addressed username = %#v", usernames[1])
	}
}

func TestParseUsernameResolution(t *testing.T) {
	list := &waBinary.Node{Tag: "list", Content: []waBinary.Node{{
		Tag:   "user",
		Attrs: waBinary.Attrs{"jid": types.NewJID("100000011111111", types.HiddenUserServer)},
		Content: []waBinary.Node{{
			Tag:   "contact",
			Attrs: waBinary.Attrs{"type": "in", "username": "example"},
		}},
	}}}
	result, err := parseUsernameResolution(list)
	if err != nil {
		t.Fatal(err)
	}
	if result.LID.String() != "100000011111111@lid" || result.Username != "example" || result.KeyRequired {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestParseUSyncUsernameFallsBackToContactAttribute(t *testing.T) {
	user := waBinary.Node{Tag: "user", Content: []waBinary.Node{{
		Tag:   "contact",
		Attrs: waBinary.Attrs{"username": "example"},
	}}}
	if got := parseUSyncUsername(user); got != "example" {
		t.Fatalf("username = %q", got)
	}
}

func TestParseUsernameResolutionDetectsRequiredKey(t *testing.T) {
	list := &waBinary.Node{Tag: "list", Content: []waBinary.Node{{
		Tag: "user",
		Content: []waBinary.Node{{
			Tag:   "contact",
			Attrs: waBinary.Attrs{"type": "in"},
		}},
	}}}
	result, err := parseUsernameResolution(list)
	if err != nil {
		t.Fatal(err)
	}
	if !result.KeyRequired {
		t.Fatal("expected username key requirement")
	}
}

func TestHistoricalInlineContactsPreferLID(t *testing.T) {
	entries, mappings := historicalInlineContactEntries([]*waHistorySync.InlineContact{{
		PnJID:    stringPtr("15550001111@s.whatsapp.net"),
		LidJID:   stringPtr("100000011111111@lid"),
		FullName: stringPtr("Example User"),
		Username: stringPtr("example"),
	}})
	if len(entries) != 1 || entries[0].JID.String() != "100000011111111@lid" || entries[0].Username != "example" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	if len(mappings) != 1 || mappings[0].LID != entries[0].JID {
		t.Fatalf("unexpected mappings: %+v", mappings)
	}
}
