// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package guest

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/util/keys"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

// NewDevice returns a fresh, unpaired device for one guest session. Its keys
// are new and everything it stores lives in memory: Signal identities,
// sessions, prekeys and sender keys work normally, other state (app state,
// contacts, chat settings) is discarded, and saving the device does nothing.
// Nothing survives the process, which is what a one-time guest identity needs.
// The store retains at most 1024 unconsumed prekeys, including at most 64 retry
// keys. Upload attempts are at least one second apart, and new upload batches
// are generated at most once per minute. These are local guest resource policies.
func NewDevice(log waLog.Logger) *store.Device {
	if log == nil {
		log = waLog.Noop
	}
	mem := newMemoryStore()
	device := &store.Device{
		Log:            log,
		NoiseKey:       keys.NewKeyPair(),
		IdentityKey:    keys.NewKeyPair(),
		RegistrationID: randomRegistrationID(),
		AdvSecretKey:   randomBytes(32),
	}
	device.SignedPreKey = device.IdentityKey.CreateSignedPreKey(1)
	device.SetAllStores(mem)
	device.LIDs = mem
	device.Container = mem
	device.Initialized = true
	return device
}

func randomRegistrationID() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	// Signal registration IDs are 14-bit and non-zero.
	id := binary.BigEndian.Uint32(b[:]) & 0x3fff
	if id == 0 {
		id = 1
	}
	return id
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// memoryStore keeps Signal state in memory and accepts everything else as a
// no-op through the embedded NoopStore (whose nil Error means success).
const (
	maxGuestPreKeys             = 1024
	maxGuestRetryPreKeys        = 64
	guestPreKeyUploadPeriod     = time.Second
	guestPreKeyGenerationPeriod = time.Minute
	maxGuestPreKeyID            = 0xffffff
)

var (
	errGuestPreKeyLimit = errors.New("guest: prekey retention or ID limit reached")
	errGuestPreKeyRate  = errors.New("guest: prekey upload rate limit reached")
)

type memoryPreKey struct {
	key      *keys.PreKey
	uploaded bool
	retry    bool
}

type memoryStore struct {
	*store.NoopStore

	mu                   sync.Mutex
	identities           map[string][32]byte
	identityDeleteGen    uint64
	sessions             map[string][]byte
	preKeys              map[uint32]memoryPreKey
	nextPreKey           uint32
	lastPreKeyUpload     time.Time
	lastPreKeyGeneration time.Time
	now                  func() time.Time
	senderKeys           map[string][]byte
}

var _ store.AllStores = (*memoryStore)(nil)
var _ store.DeviceContainer = (*memoryStore)(nil)

func newMemoryStore() *memoryStore {
	return &memoryStore{
		NoopStore:  &store.NoopStore{},
		identities: make(map[string][32]byte),
		sessions:   make(map[string][]byte),
		preKeys:    make(map[uint32]memoryPreKey),
		nextPreKey: 1,
		now:        time.Now,
		senderKeys: make(map[string][]byte),
	}
}

func addressUser(address string) string {
	user, _, _ := strings.Cut(address, ".")
	user, _, _ = strings.Cut(user, ":")
	return user
}

// IdentityStore

func (m *memoryStore) PutIdentity(_ context.Context, address string, key [32]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identities[address] = key
	return nil
}

func (m *memoryStore) DeleteAllIdentities(_ context.Context, phone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identityDeleteGen++
	for address := range m.identities {
		if addressUser(address) == phone {
			delete(m.identities, address)
		}
	}
	return nil
}

func (m *memoryStore) DeleteIdentity(_ context.Context, address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identityDeleteGen++
	delete(m.identities, address)
	return nil
}

func (m *memoryStore) IsTrustedIdentity(_ context.Context, address string, key [32]byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	known, ok := m.identities[address]
	return !ok || known == key, nil
}

func (m *memoryStore) GetManyIdentities(_ context.Context, addresses []string) (map[string][32]byte, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][32]byte, len(addresses))
	for _, address := range addresses {
		if key, ok := m.identities[address]; ok {
			out[address] = key
		}
	}
	return out, m.identityDeleteGen, nil
}

func (m *memoryStore) EnsureIdentity(_ context.Context, address string, key [32]byte, deleteGeneration uint64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if deleteGeneration != m.identityDeleteGen {
		return false, nil
	}
	if known, ok := m.identities[address]; ok {
		return known == key, nil
	}
	m.identities[address] = key
	return true, nil
}

// SessionStore

func (m *memoryStore) GetSession(_ context.Context, address string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.sessions[address]...), nil
}

func (m *memoryStore) HasSession(_ context.Context, address string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[address]
	return ok, nil
}

func (m *memoryStore) GetManySessions(_ context.Context, addresses []string) (map[string][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(addresses))
	for _, address := range addresses {
		if session, ok := m.sessions[address]; ok {
			out[address] = append([]byte(nil), session...)
		}
	}
	return out, nil
}

func (m *memoryStore) PutSession(_ context.Context, address string, session []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[address] = append([]byte(nil), session...)
	return nil
}

func (m *memoryStore) PutManySessions(_ context.Context, sessions map[string][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for address, session := range sessions {
		m.sessions[address] = append([]byte(nil), session...)
	}
	return nil
}

func (m *memoryStore) DeleteAllSessions(_ context.Context, phone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for address := range m.sessions {
		if addressUser(address) == phone {
			delete(m.sessions, address)
		}
	}
	return nil
}

func (m *memoryStore) DeleteSession(_ context.Context, address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, address)
	return nil
}

func (m *memoryStore) MigratePNToLID(context.Context, types.JID, types.JID) error {
	return nil
}

// PreKeyStore

func (m *memoryStore) GetOrGenPreKeys(_ context.Context, count uint32) ([]*keys.PreKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if count == 0 {
		return []*keys.PreKey{}, nil
	}
	if count > maxGuestPreKeys {
		return nil, errGuestPreKeyLimit
	}
	now := m.now()
	if !m.lastPreKeyUpload.IsZero() && now.Before(m.lastPreKeyUpload.Add(guestPreKeyUploadPeriod)) {
		return nil, errGuestPreKeyRate
	}
	pending := make([]*keys.PreKey, 0, len(m.preKeys))
	for _, entry := range m.preKeys {
		if !entry.uploaded {
			pending = append(pending, entry.key)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].KeyID < pending[j].KeyID })
	if len(pending) > int(count) {
		pending = pending[:count]
	}
	needed := int(count) - len(pending)
	if needed > 0 && !m.lastPreKeyGeneration.IsZero() && now.Before(m.lastPreKeyGeneration.Add(guestPreKeyGenerationPeriod)) {
		return nil, errGuestPreKeyRate
	}
	if needed > maxGuestPreKeys-len(m.preKeys) || uint64(m.nextPreKey)+uint64(needed) > maxGuestPreKeyID+1 {
		return nil, errGuestPreKeyLimit
	}
	m.lastPreKeyUpload = now
	out := pending
	for len(out) < int(count) {
		key, err := m.newPreKeyLocked(false)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	if needed > 0 {
		m.lastPreKeyGeneration = now
	}
	return out, nil
}

func (m *memoryStore) GenOnePreKey(_ context.Context) (*keys.PreKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.newPreKeyLocked(true)
}

func (m *memoryStore) newPreKeyLocked(retry bool) (*keys.PreKey, error) {
	if len(m.preKeys) >= maxGuestPreKeys || m.nextPreKey > maxGuestPreKeyID {
		return nil, errGuestPreKeyLimit
	}
	if retry {
		count := 0
		for _, entry := range m.preKeys {
			if entry.retry {
				count++
			}
		}
		if count >= maxGuestRetryPreKeys {
			return nil, errGuestPreKeyLimit
		}
	}
	key := keys.NewPreKey(m.nextPreKey)
	m.preKeys[key.KeyID] = memoryPreKey{key: key, uploaded: retry, retry: retry}
	m.nextPreKey++
	return key, nil
}

func (m *memoryStore) GetPreKey(_ context.Context, id uint32) (*keys.PreKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preKeys[id].key, nil
}

func (m *memoryStore) RemovePreKey(_ context.Context, id uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.preKeys, id)
	return nil
}

func (m *memoryStore) MarkPreKeysAsUploaded(_ context.Context, upToID uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, entry := range m.preKeys {
		if id <= upToID {
			entry.uploaded = true
			m.preKeys[id] = entry
		}
	}
	return nil
}

func (m *memoryStore) UploadedPreKeyCount(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, entry := range m.preKeys {
		if entry.uploaded {
			count++
		}
	}
	return count, nil
}

// SenderKeyStore

func (m *memoryStore) PutSenderKey(_ context.Context, group, user string, session []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.senderKeys[group+"\x00"+user] = append([]byte(nil), session...)
	return nil
}

func (m *memoryStore) GetSenderKey(_ context.Context, group, user string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.senderKeys[group+"\x00"+user]...), nil
}
