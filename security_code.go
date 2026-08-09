package whatsmeow

import (
	"bytes"
	"context"
	"crypto/sha512"
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow/proto/waFingerprint"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

const securityCodeIterations = 5200

var (
	ErrIdentityKeyReaderUnsupported    = errors.New("identity store does not support batch identity-key reads")
	ErrIdentityVerificationRequiresLID = errors.New("identity verification requires an @lid user ID")
)

type IdentityVerificationCodes struct {
	UserID             types.JID
	PhoneNumber        types.JID
	Username           string
	NumericCode        string
	DisplayQRCode      []byte
	VerificationQRCode []byte
}

type identityVerificationFingerprint struct {
	LID      types.JID
	Phone    types.JID
	Username string
	Keys     [][32]byte
	Hosted   bool
}

func newIdentityVerificationCodes(ctx context.Context, local, remote identityVerificationFingerprint) (*IdentityVerificationCodes, error) {
	numericCode, err := generateNumericSecurityCode(ctx, []byte(local.LID.User), local.Keys, []byte(remote.LID.User), remote.Keys)
	if err != nil {
		return nil, err
	}
	displayQR, verificationQR, err := buildIdentityVerificationQRCodes(local, remote)
	if err != nil {
		return nil, err
	}
	return &IdentityVerificationCodes{
		UserID:             remote.LID,
		PhoneNumber:        remote.Phone,
		Username:           remote.Username,
		NumericCode:        numericCode,
		DisplayQRCode:      displayQR,
		VerificationQRCode: verificationQR,
	}, nil
}

func (cli *Client) GetIdentityVerificationCodes(ctx context.Context, userID types.JID) (*IdentityVerificationCodes, error) {
	if cli == nil || cli.Store == nil {
		return nil, ErrClientIsNil
	}
	userID = userID.ToNonAD()
	if userID.Server != types.HiddenUserServer || userID.User == "" {
		return nil, ErrIdentityVerificationRequiresLID
	}
	localLID := cli.Store.LID.ToNonAD()
	if localLID.Server != types.HiddenUserServer || localLID.User == "" {
		return nil, errors.New("local LID is unavailable")
	}
	if localLID == userID {
		return nil, errors.New("cannot generate an identity verification code for the local user")
	}
	if cli.Store.IdentityKey == nil || cli.Store.IdentityKey.Pub == nil {
		return nil, errors.New("local identity key is unavailable")
	}

	devices, err := cli.GetUserDevices(ctx, []types.JID{localLID, userID})
	if err != nil {
		return nil, fmt.Errorf("get identity verification devices: %w", err)
	}
	var currentDevice uint16
	hasCurrentDevice := cli.Store.ID != nil
	if cli.Store.ID != nil {
		currentDevice = cli.Store.ID.Device
	}
	localDevices, remoteDevices := splitIdentityVerificationDevices(devices, localLID, userID, currentDevice, hasCurrentDevice)
	if len(remoteDevices) == 0 {
		return nil, fmt.Errorf("no devices found for %s", userID)
	}
	localKeys := make([][32]byte, 1, len(localDevices)+1)
	localKeys[0] = *cli.Store.IdentityKey.Pub
	otherLocalKeys, err := cli.readIdentityKeys(ctx, localDevices)
	if err != nil {
		return nil, fmt.Errorf("read local device identities: %w", err)
	}
	localKeys = append(localKeys, otherLocalKeys...)
	remoteKeys, err := cli.readIdentityKeys(ctx, remoteDevices)
	if err != nil {
		return nil, fmt.Errorf("read remote device identities: %w", err)
	}

	local := identityVerificationFingerprint{LID: localLID, Keys: localKeys}
	remote := identityVerificationFingerprint{LID: userID, Keys: remoteKeys}
	local.Hosted = hasHostedIdentityDevice(localDevices)
	remote.Hosted = hasHostedIdentityDevice(remoteDevices)
	if cli.Store.ID != nil && cli.Store.ID.Server == types.DefaultUserServer {
		local.Phone = cli.Store.ID.ToNonAD()
	}
	if cli.Store.LIDs != nil {
		remote.Phone, err = cli.Store.LIDs.GetPNForLID(ctx, userID)
		if err != nil {
			cli.Log.Warnf("Failed to get phone-number alias for %s: %v", userID, err)
			remote.Phone = types.EmptyJID
		}
	}
	if cli.Store.Contacts != nil {
		if contact, contactErr := cli.Store.Contacts.GetContact(ctx, localLID); contactErr == nil {
			local.Username = contact.Username
		}
		if contact, contactErr := cli.Store.Contacts.GetContact(ctx, userID); contactErr == nil {
			remote.Username = contact.Username
		}
	}
	return newIdentityVerificationCodes(ctx, local, remote)
}

func splitIdentityVerificationDevices(devices []types.JID, localLID, remoteLID types.JID, currentDevice uint16, hasCurrentDevice bool) ([]types.JID, []types.JID) {
	localDevices := make([]types.JID, 0, len(devices))
	remoteDevices := make([]types.JID, 0, len(devices))
	for _, device := range devices {
		switch {
		case device.User == localLID.User && (device.Server == types.HiddenUserServer || device.Server == types.HostedLIDServer):
			if !hasCurrentDevice || device.Device != currentDevice {
				localDevices = append(localDevices, device)
			}
		case device.User == remoteLID.User && (device.Server == types.HiddenUserServer || device.Server == types.HostedLIDServer):
			remoteDevices = append(remoteDevices, device)
		}
	}
	return localDevices, remoteDevices
}

func hasHostedIdentityDevice(devices []types.JID) bool {
	return slices.ContainsFunc(devices, func(device types.JID) bool {
		return device.Server == types.HostedLIDServer || device.Server == types.HostedServer
	})
}

func (cli *Client) readIdentityKeys(ctx context.Context, devices []types.JID) ([][32]byte, error) {
	if cli == nil || cli.Store == nil {
		return nil, ErrClientIsNil
	}
	reader, ok := cli.Store.Identities.(store.IdentityKeyReader)
	if !ok {
		return nil, ErrIdentityKeyReaderUnsupported
	}
	addresses := make([]string, 0, len(devices))
	deviceByAddress := make(map[string]types.JID, len(devices))
	for _, device := range devices {
		address := device.SignalAddress().String()
		if _, exists := deviceByAddress[address]; exists {
			continue
		}
		addresses = append(addresses, address)
		deviceByAddress[address] = device
	}
	stored, err := reader.GetManyIdentities(ctx, addresses)
	if err != nil {
		return nil, fmt.Errorf("read identity keys: %w", err)
	}
	if stored == nil {
		stored = make(map[string][32]byte, len(addresses))
	}
	missing := make([]types.JID, 0, len(addresses))
	for _, address := range addresses {
		if _, exists := stored[address]; !exists {
			missing = append(missing, deviceByAddress[address])
		}
	}
	if len(missing) > 0 {
		responses, fetchErr := cli.fetchPreKeys(ctx, missing)
		if fetchErr != nil {
			return nil, fetchErr
		}
		for _, device := range missing {
			response, exists := responses[device]
			if !exists {
				return nil, fmt.Errorf("identity key missing from prekey response for %s", device)
			}
			if response.err != nil {
				return nil, fmt.Errorf("fetch identity key for %s: %w", device, response.err)
			}
			key := response.bundle.IdentityKey().PublicKey().PublicKey()
			address := device.SignalAddress().String()
			trusted, trustErr := cli.Store.Identities.IsTrustedIdentity(ctx, address, key)
			if trustErr != nil {
				return nil, fmt.Errorf("check identity key for %s: %w", device, trustErr)
			}
			if !trusted {
				return nil, fmt.Errorf("identity key for %s is not trusted", device)
			}
			if putErr := cli.Store.Identities.PutIdentity(ctx, address, key); putErr != nil {
				return nil, fmt.Errorf("store identity key for %s: %w", device, putErr)
			}
			stored[address] = key
		}
	}
	keys := make([][32]byte, 0, len(addresses))
	for _, address := range addresses {
		key, exists := stored[address]
		if !exists {
			return nil, fmt.Errorf("identity key unavailable for %s", deviceByAddress[address])
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func generateNumericSecurityCode(
	ctx context.Context,
	localIdentifier []byte,
	localKeys [][32]byte,
	remoteIdentifier []byte,
	remoteKeys [][32]byte,
) (string, error) {
	local, err := generateSecurityCodeFingerprint(ctx, localIdentifier, serializeIdentityKeys(localKeys))
	if err != nil {
		return "", err
	}
	remote, err := generateSecurityCodeFingerprint(ctx, remoteIdentifier, serializeIdentityKeys(remoteKeys))
	if err != nil {
		return "", err
	}
	if local < remote {
		return local + remote, nil
	}
	return remote + local, nil
}

func serializeIdentityKeys(keys [][32]byte) []byte {
	keys = slices.Clone(keys)
	slices.SortFunc(keys, func(left, right [32]byte) int {
		return bytes.Compare(left[:], right[:])
	})
	serialized := make([]byte, 0, len(keys)*33)
	for _, key := range keys {
		serialized = append(serialized, 0x05)
		serialized = append(serialized, key[:]...)
	}
	return serialized
}

func buildIdentityVerificationQRCodes(local, remote identityVerificationFingerprint) ([]byte, []byte, error) {
	if err := validateIdentityVerificationFingerprint(local); err != nil {
		return nil, nil, fmt.Errorf("invalid local fingerprint: %w", err)
	}
	if err := validateIdentityVerificationFingerprint(remote); err != nil {
		return nil, nil, fmt.Errorf("invalid remote fingerprint: %w", err)
	}
	display, err := marshalCombinedFingerprint(local, remote, false)
	if err != nil {
		return nil, nil, err
	}
	verify, err := marshalCombinedFingerprint(local, remote, true)
	if err != nil {
		return nil, nil, err
	}
	return display, verify, nil
}

func validateIdentityVerificationFingerprint(fingerprint identityVerificationFingerprint) error {
	if fingerprint.LID.Server != types.HiddenUserServer || fingerprint.LID.User == "" {
		return errors.New("LID is required")
	}
	if !fingerprint.Phone.IsEmpty() && fingerprint.Phone.Server != types.DefaultUserServer {
		return errors.New("phone number must be a phone-number JID")
	}
	if len(fingerprint.Keys) == 0 {
		return errors.New("at least one identity key is required")
	}
	return nil
}

func marshalCombinedFingerprint(local, remote identityVerificationFingerprint, includeUnhashed bool) ([]byte, error) {
	return proto.Marshal(&waFingerprint.CombinedFingerprint{
		Version:           proto.Uint32(1),
		LocalFingerprint:  buildFingerprintData(local, includeUnhashed),
		RemoteFingerprint: buildFingerprintData(remote, includeUnhashed),
	})
}

func buildFingerprintData(fingerprint identityVerificationFingerprint, includeUnhashed bool) *waFingerprint.FingerprintData {
	serialized := serializeIdentityKeys(fingerprint.Keys)
	hash := sha512.Sum512(serialized)
	hostedState := waFingerprint.HostedState_E2EE
	if fingerprint.Hosted {
		hostedState = waFingerprint.HostedState_HOSTED
	}
	data := &waFingerprint.FingerprintData{
		LidIdentifier:      []byte(fingerprint.LID.String()),
		UsernameIdentifier: []byte(fingerprint.Username),
		HostedState:        &hostedState,
		HashedPublicKey:    hash[:],
	}
	if !fingerprint.Phone.IsEmpty() {
		data.PnIdentifier = []byte(fingerprint.Phone.User)
	}
	if includeUnhashed {
		data.PublicKey = serialized
	}
	return data
}

func generateSecurityCodeFingerprint(ctx context.Context, identifier, keys []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	input := make([]byte, 2, 2+len(keys)+len(identifier))
	input = append(input, keys...)
	input = append(input, identifier...)
	digest := sha512.Sum512(input)
	input = make([]byte, 0, len(digest)+len(keys))
	for index := 0; index < securityCodeIterations; index++ {
		if index%64 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		input = append(input[:0], digest[:]...)
		input = append(input, keys...)
		digest = sha512.Sum512(input)
	}
	code := make([]byte, 0, 30)
	for offset := 0; offset < 30; offset += 5 {
		value := uint64(digest[offset])<<32 |
			uint64(digest[offset+1])<<24 |
			uint64(digest[offset+2])<<16 |
			uint64(digest[offset+3])<<8 |
			uint64(digest[offset+4])
		code = fmt.Appendf(code, "%05d", value%100000)
	}
	return string(code), nil
}
