// Copyright (c) 2022 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/polymorfa/libsignal-protocol-go/ecc"
	groupRecord "github.com/polymorfa/libsignal-protocol-go/groups/state/record"
	"github.com/polymorfa/libsignal-protocol-go/keys/identity"
	"github.com/polymorfa/libsignal-protocol-go/protocol"
	"github.com/polymorfa/libsignal-protocol-go/serialize"
	"github.com/polymorfa/libsignal-protocol-go/state/record"
	"github.com/polymorfa/libsignal-protocol-go/state/store"
)

var SignalProtobufSerializer = serialize.NewProtoBufSerializer()

var ErrUnsupportedSignalStoreOperation = errors.New("signal store operation is not supported by the WhatsApp device store")

var _ store.SignalProtocol = (*Device)(nil)

// errRemoteKeys explains a local Signal operation reached on a device whose
// private keys are remote. Reaching it is a programming error.
const errRemoteKeys = "whatsmeow: local Signal operation on a device with remote keys"

func (device *Device) GetIdentityKeyPair() *identity.KeyPair {
	if device.RemoteKeys {
		panic(errRemoteKeys + ": identity key")
	}
	return identity.NewKeyPair(
		identity.NewKey(ecc.NewDjbECPublicKey(*device.IdentityKey.Pub)),
		ecc.NewDjbECPrivateKey(*device.IdentityKey.Priv),
	)
}

func (device *Device) GetLocalRegistrationID() uint32 {
	return device.RegistrationID
}

func (device *Device) SaveIdentity(ctx context.Context, address *protocol.SignalAddress, identityKey *identity.Key) error {
	addrString := address.String()
	err := device.Identities.PutIdentity(ctx, addrString, identityKey.PublicKey().PublicKey())
	if err != nil {
		return fmt.Errorf("failed to save identity of %s: %w", addrString, err)
	}
	return nil
}

func (device *Device) IsTrustedIdentity(ctx context.Context, address *protocol.SignalAddress, identityKey *identity.Key) (bool, error) {
	addrString := address.String()
	isTrusted, err := device.Identities.IsTrustedIdentity(ctx, addrString, identityKey.PublicKey().PublicKey())
	if err != nil {
		return false, fmt.Errorf("failed to check if %s's identity is trusted: %w", addrString, err)
	}
	return isTrusted, nil
}

func (device *Device) LoadPreKey(ctx context.Context, id uint32) (*record.PreKey, error) {
	preKey, err := device.PreKeys.GetPreKey(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load prekey %d: %w", id, err)
	}
	if preKey == nil {
		return nil, nil
	}
	return record.NewPreKey(preKey.KeyID, ecc.NewECKeyPair(
		ecc.NewDjbECPublicKey(*preKey.Pub),
		ecc.NewDjbECPrivateKey(*preKey.Priv),
	), nil), nil
}

func (device *Device) RemovePreKey(ctx context.Context, id uint32) error {
	err := device.PreKeys.RemovePreKey(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to remove prekey %d: %w", id, err)
	}
	return nil
}

func (device *Device) StorePreKey(ctx context.Context, preKeyID uint32, preKeyRecord *record.PreKey) error {
	return fmt.Errorf("StorePreKey: %w", ErrUnsupportedSignalStoreOperation)
}

func (device *Device) ContainsPreKey(ctx context.Context, preKeyID uint32) (bool, error) {
	preKey, err := device.PreKeys.GetPreKey(ctx, preKeyID)
	if err != nil {
		return false, fmt.Errorf("failed to check prekey %d: %w", preKeyID, err)
	}
	return preKey != nil, nil
}

func (device *Device) LoadSession(ctx context.Context, address *protocol.SignalAddress) (*record.Session, error) {
	var sess *record.Session
	err := device.WithSessionStateLock(ctx, func(ctx context.Context) (err error) {
		sess, err = device.loadSession(ctx, address)
		return
	})
	return sess, err
}

func (device *Device) loadSession(ctx context.Context, address *protocol.SignalAddress) (*record.Session, error) {
	addrString := SignalSessionAddress(ctx, address.String())
	if err := device.checkCachedSession(ctx, addrString); err != nil {
		return nil, err
	}
	if sess := getCachedSession(ctx, device, addrString); sess != nil {
		return sess, nil
	}

	var sess *record.Session
	load := func(rawSess []byte) error {
		var err error
		sess, err = record.NewSessionFromBytes(rawSess, SignalProtobufSerializer.Session, SignalProtobufSerializer.State)
		return err
	}
	if loader, ok := device.Sessions.(sessionLoader); ok {
		found, err := loader.IterateSession(ctx, addrString, load)
		if err != nil {
			return nil, fmt.Errorf("failed to load session with %s: %w", addrString, err)
		}
		if !found {
			return record.NewSession(SignalProtobufSerializer.Session, SignalProtobufSerializer.State), nil
		}
	} else {
		rawSess, err := device.Sessions.GetSession(ctx, addrString)
		if err != nil {
			return nil, fmt.Errorf("failed to load session with %s: %w", addrString, err)
		}
		if rawSess == nil {
			return record.NewSession(SignalProtobufSerializer.Session, SignalProtobufSerializer.State), nil
		}
		if err = load(rawSess); err != nil {
			return nil, fmt.Errorf("failed to deserialize session with %s: %w", addrString, err)
		}
	}
	return sess, nil
}

func (device *Device) GetSubDeviceSessions(ctx context.Context, name string) ([]uint32, error) {
	return nil, fmt.Errorf("GetSubDeviceSessions: %w", ErrUnsupportedSignalStoreOperation)
}

func (device *Device) StoreSession(ctx context.Context, address *protocol.SignalAddress, record *record.Session) error {
	return device.WithSessionStateLock(ctx, func(ctx context.Context) error {
		return device.storeSession(ctx, address, record)
	})
}

func (device *Device) storeSession(ctx context.Context, address *protocol.SignalAddress, record *record.Session) error {
	addrString := SignalSessionAddress(ctx, address.String())
	if err := device.checkCachedSession(ctx, addrString); err != nil {
		return err
	}
	if putCachedSession(ctx, device, addrString, record) {
		return nil
	}

	err := device.Sessions.PutSession(ctx, addrString, record.Serialize())
	if err != nil {
		return fmt.Errorf("failed to store session with %s: %w", addrString, err)
	}
	return nil
}

func (device *Device) ContainsSession(ctx context.Context, remoteAddress *protocol.SignalAddress) (bool, error) {
	var found bool
	err := device.WithSessionStateLock(ctx, func(ctx context.Context) (err error) {
		found, err = device.containsSession(ctx, remoteAddress)
		return
	})
	return found, err
}

func (device *Device) containsSession(ctx context.Context, remoteAddress *protocol.SignalAddress) (bool, error) {
	addrString := SignalSessionAddress(ctx, remoteAddress.String())
	if err := device.checkCachedSession(ctx, addrString); err != nil {
		return false, err
	}
	if found, cached := hasCachedSession(ctx, device, addrString); cached {
		return found, nil
	}
	hasSession, err := device.Sessions.HasSession(ctx, addrString)
	if err != nil {
		return false, fmt.Errorf("failed to check if store has session for %s: %w", addrString, err)
	}
	return hasSession, nil
}

func (device *Device) DeleteSession(ctx context.Context, remoteAddress *protocol.SignalAddress) error {
	return device.WithSessionStateLock(ctx, func(ctx context.Context) error {
		addrString := SignalSessionAddress(ctx, remoteAddress.String())
		if err := device.Sessions.DeleteSession(ctx, addrString); err != nil {
			return fmt.Errorf("failed to delete session with %s: %w", addrString, err)
		}
		delete(device.sessionResetPending, addrString)
		return nil
	})
}

// DeleteSessionsForIdentity removes both pairwise scopes after a shared identity change.
func (device *Device) DeleteSessionsForIdentity(ctx context.Context, remoteAddress *protocol.SignalAddress) error {
	return device.WithSessionStateLock(ctx, func(ctx context.Context) error {
		keys := []string{remoteAddress.String(), remoteAddress.String() + "_status"}
		device.sessionResetEpoch++
		if device.sessionResetAddresses == nil {
			device.sessionResetAddresses = make(map[string]uint64)
		}
		if len(device.sessionResetAddresses)+len(keys) > maxSessionResetAddresses {
			// Forgetting a reset must never make an outstanding cache valid.
			// Caches older than this floor must be prefetched again.
			device.sessionResetFloor = device.sessionResetEpoch
			clear(device.sessionResetAddresses)
		}
		if device.sessionResetPending == nil {
			device.sessionResetPending = make(map[string]struct{})
		}
		for _, key := range keys {
			device.sessionResetAddresses[key] = device.sessionResetEpoch
			if _, pending := device.sessionResetPending[key]; !pending {
				if len(device.sessionResetPending) >= maxSessionResetAddresses {
					// Unknown failed keys cannot be forgotten safely. Refuse
					// this Device until its backend state is reconciled.
					device.sessionResetBlocked = true
				} else {
					device.sessionResetPending[key] = struct{}{}
				}
			}
		}
		var deleteErr error
		for _, key := range keys {
			if err := device.Sessions.DeleteSession(ctx, key); err != nil {
				deleteErr = errors.Join(deleteErr, fmt.Errorf("failed to delete session with %s: %w", key, err))
			} else {
				delete(device.sessionResetPending, key)
			}
		}
		if deleteErr != nil {
			return errors.Join(ErrSessionCacheInvalidated, deleteErr)
		}
		if device.sessionResetBlocked {
			return ErrSessionCacheInvalidated
		}
		if cache := getSessionCache(ctx, device); cache != nil {
			for _, key := range keys {
				cache.Set(key, sessionCacheEntry{Generation: device.sessionResetEpoch, Record: record.NewSession(SignalProtobufSerializer.Session, SignalProtobufSerializer.State)})
			}
		}
		return nil
	})
}

func (device *Device) DeleteAllSessions(ctx context.Context) error {
	return fmt.Errorf("DeleteAllSessions: %w", ErrUnsupportedSignalStoreOperation)
}

func (device *Device) LoadSignedPreKey(ctx context.Context, signedPreKeyID uint32) (*record.SignedPreKey, error) {
	if device.SignedPreKey == nil {
		return nil, nil
	}
	if signedPreKeyID == device.SignedPreKey.KeyID {
		if device.RemoteKeys {
			panic(errRemoteKeys + ": signed prekey")
		}
		return record.NewSignedPreKey(signedPreKeyID, 0, ecc.NewECKeyPair(
			ecc.NewDjbECPublicKey(*device.SignedPreKey.Pub),
			ecc.NewDjbECPrivateKey(*device.SignedPreKey.Priv),
		), *device.SignedPreKey.Signature, nil), nil
	}
	return nil, nil
}

func (device *Device) LoadSignedPreKeys(ctx context.Context) ([]*record.SignedPreKey, error) {
	if device.SignedPreKey == nil {
		return nil, nil
	}
	preKey, err := device.LoadSignedPreKey(ctx, device.SignedPreKey.KeyID)
	if err != nil || preKey == nil {
		return nil, err
	}
	return []*record.SignedPreKey{preKey}, nil
}

func (device *Device) StoreSignedPreKey(ctx context.Context, signedPreKeyID uint32, record *record.SignedPreKey) error {
	return fmt.Errorf("StoreSignedPreKey: %w", ErrUnsupportedSignalStoreOperation)
}

func (device *Device) ContainsSignedPreKey(ctx context.Context, signedPreKeyID uint32) (bool, error) {
	return device.SignedPreKey != nil && signedPreKeyID == device.SignedPreKey.KeyID, nil
}

func (device *Device) RemoveSignedPreKey(ctx context.Context, signedPreKeyID uint32) error {
	return fmt.Errorf("RemoveSignedPreKey: %w", ErrUnsupportedSignalStoreOperation)
}

func (device *Device) StoreSenderKey(ctx context.Context, senderKeyName *protocol.SenderKeyName, keyRecord *groupRecord.SenderKey) error {
	groupID := senderKeyName.GroupID()
	senderString := senderKeyName.Sender().String()
	err := device.SenderKeys.PutSenderKey(ctx, groupID, senderString, keyRecord.Serialize())
	if err != nil {
		return fmt.Errorf("failed to store sender key from %s for %s: %w", senderString, groupID, err)
	}
	return nil
}

func (device *Device) LoadSenderKey(ctx context.Context, senderKeyName *protocol.SenderKeyName) (*groupRecord.SenderKey, error) {
	groupID := senderKeyName.GroupID()
	senderString := senderKeyName.Sender().String()
	rawKey, err := device.SenderKeys.GetSenderKey(ctx, groupID, senderString)
	if err != nil {
		return nil, fmt.Errorf("failed to load sender key from %s for %s: %w", senderString, groupID, err)
	}
	if rawKey == nil {
		return groupRecord.NewSenderKey(SignalProtobufSerializer.SenderKeyRecord, SignalProtobufSerializer.SenderKeyState), nil
	}
	key, err := groupRecord.NewSenderKeyFromBytes(rawKey, SignalProtobufSerializer.SenderKeyRecord, SignalProtobufSerializer.SenderKeyState)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize sender key from %s for %s: %w", senderString, groupID, err)
	}
	return key, nil
}
