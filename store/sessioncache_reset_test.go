// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package store

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/polymorfa/libsignal-protocol-go/keys/chain"
	"github.com/polymorfa/libsignal-protocol-go/protocol"
	"github.com/polymorfa/libsignal-protocol-go/state/record"
)

type resetSessionStore struct {
	countingSessionStore
	mu         sync.Mutex
	rows       map[string][]byte
	failDelete string
	beforePut  func()
}

func (s *resetSessionStore) GetManySessions(_ context.Context, addresses []string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte)
	for _, address := range addresses {
		out[address] = bytes.Clone(s.rows[address])
	}
	return out, nil
}

func (s *resetSessionStore) PutManySessions(_ context.Context, rows map[string][]byte) error {
	if s.beforePut != nil {
		s.beforePut()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for address, data := range rows {
		s.rows[address] = bytes.Clone(data)
	}
	return nil
}

func (s *resetSessionStore) DeleteSession(_ context.Context, address string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDelete == address {
		return errors.New("authored delete failure")
	}
	delete(s.rows, address)
	return nil
}

// Authored serialization fixture only; these records are never used to encrypt.
func cacheTestSession() *record.Session {
	public := append([]byte{5}, bytes.Repeat([]byte{1}, 32)...)
	sess, err := record.NewSessionFromStructure(&record.SessionStructure{SessionState: &record.StateStructure{
		LocalIdentityPublic: public, RemoteIdentityPublic: public, SenderBaseKey: public,
		RootKey: make([]byte, 32), SessionVersion: 3,
		SenderChain: &record.ChainStructure{SenderRatchetKeyPublic: public, SenderRatchetKeyPrivate: make([]byte, 32), ChainKey: &chain.KeyStructure{Key: make([]byte, 32)}},
	}}, SignalProtobufSerializer.Session, SignalProtobufSerializer.State)
	if err != nil {
		panic(err)
	}
	return sess
}

func TestIdentityResetRejectsAnIndependentDirtyCache(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial deletion"}[partial], func(t *testing.T) {
			address := protocol.NewSignalAddress("123", 1)
			keys := []string{address.String(), address.String() + "_status", "unrelated:2"}
			backend := &resetSessionStore{rows: make(map[string][]byte)}
			for _, key := range keys {
				backend.rows[key] = cacheTestSession().Serialize()
			}
			device := &Device{Sessions: backend}
			_, cached, err := device.WithCachedSessions(context.Background(), keys)
			if err != nil {
				t.Fatal(err)
			}
			if err = device.StoreSession(cached, address, cacheTestSession()); err != nil {
				t.Fatal(err)
			}
			if err = device.StoreSession(WithStatusSession(cached), address, cacheTestSession()); err != nil {
				t.Fatal(err)
			}
			if partial {
				backend.failDelete = keys[1]
			}
			err = device.DeleteSessionsForIdentity(context.Background(), address)
			if (err != nil) != partial {
				t.Fatalf("reset error = %v", err)
			}
			if err = device.PutCachedSessions(cached); err == nil {
				t.Fatal("stale cache flush accepted after identity reset")
			}
			if _, exists := backend.rows[keys[0]]; exists {
				t.Fatal("flush restored deleted chat session")
			}
			if _, exists := backend.rows[keys[1]]; exists != partial {
				t.Fatal("flush changed status session after reset")
			}
		})
	}
}

func TestIdentityResetKeepsUnrelatedAndFreshCachesUsable(t *testing.T) {
	address := protocol.NewSignalAddress("123", 1)
	other := protocol.NewSignalAddress("456", 2)
	backend := &resetSessionStore{rows: make(map[string][]byte)}
	device := &Device{Sessions: backend}
	_, unrelated, err := device.WithCachedSessions(context.Background(), []string{other.String()})
	if err != nil {
		t.Fatal(err)
	}
	if err = device.StoreSession(unrelated, other, cacheTestSession()); err != nil {
		t.Fatal(err)
	}
	if err = device.DeleteSessionsForIdentity(context.Background(), address); err != nil {
		t.Fatal(err)
	}
	if err = device.PutCachedSessions(unrelated); err != nil {
		t.Fatal(err)
	}
	_, fresh, err := device.WithCachedSessions(context.Background(), []string{address.String(), address.String() + "_status"})
	if err != nil {
		t.Fatal(err)
	}
	if err = device.StoreSession(fresh, address, cacheTestSession()); err != nil {
		t.Fatal(err)
	}
	if err = device.StoreSession(WithStatusSession(fresh), address, cacheTestSession()); err != nil {
		t.Fatal(err)
	}
	if err = device.PutCachedSessions(fresh); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{address.String(), address.String() + "_status", other.String()} {
		if _, ok := backend.rows[key]; !ok {
			t.Fatalf("missing fresh session %s", key)
		}
	}
}

func TestIdentityResetCanRebuildWithinItsOwnCache(t *testing.T) {
	address := protocol.NewSignalAddress("123", 1)
	backend := &resetSessionStore{rows: make(map[string][]byte)}
	device := &Device{Sessions: backend}
	_, cached, err := device.WithCachedSessions(context.Background(), []string{address.String()})
	if err != nil {
		t.Fatal(err)
	}
	if err = device.DeleteSessionsForIdentity(cached, address); err != nil {
		t.Fatal(err)
	}
	if err = device.StoreSession(cached, address, cacheTestSession()); err != nil {
		t.Fatal(err)
	}
	if err = device.PutCachedSessions(cached); err != nil {
		t.Fatal(err)
	}
	if _, ok := backend.rows[address.String()]; !ok {
		t.Fatal("freshly rebuilt session was discarded")
	}
}

func TestIdentityResetOrdersDeletionAfterAnActiveFlush(t *testing.T) {
	address := protocol.NewSignalAddress("123", 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	backend := &resetSessionStore{rows: make(map[string][]byte), beforePut: func() { close(entered); <-release }}
	device := &Device{Sessions: backend}
	_, cached, err := device.WithCachedSessions(context.Background(), []string{address.String()})
	if err != nil {
		t.Fatal(err)
	}
	if err = device.StoreSession(cached, address, cacheTestSession()); err != nil {
		t.Fatal(err)
	}
	flushed := make(chan error, 1)
	go func() { flushed <- device.PutCachedSessions(cached) }()
	<-entered
	reset := make(chan error, 1)
	go func() { reset <- device.DeleteSessionsForIdentity(context.Background(), address) }()
	var resetErr error
	finished := false
	select {
	case resetErr = <-reset:
		finished = true
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	if err = <-flushed; err != nil {
		t.Fatal(err)
	}
	if !finished {
		resetErr = <-reset
	}
	if resetErr != nil {
		t.Fatal(resetErr)
	}
	if _, exists := backend.rows[address.String()]; exists {
		t.Fatal("active flush restored a session after reset completed")
	}
}
