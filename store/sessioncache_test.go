package store

import (
	"context"
	"testing"

	"github.com/polymorfa/libsignal-protocol-go/protocol"
	"go.mau.fi/whatsmeow/types"
)

type countingSessionStore struct {
	hasCalls     int
	getCalls     int
	getManyCalls int
}

func (s *countingSessionStore) GetSession(context.Context, string) ([]byte, error) {
	s.getCalls++
	return nil, nil
}
func (s *countingSessionStore) HasSession(context.Context, string) (bool, error) {
	s.hasCalls++
	return false, nil
}
func (s *countingSessionStore) GetManySessions(_ context.Context, addresses []string) (map[string][]byte, error) {
	s.getManyCalls++
	result := make(map[string][]byte, len(addresses))
	for _, address := range addresses {
		result[address] = nil
	}
	return result, nil
}

type iteratingSessionStore struct {
	countingSessionStore
	iterateCalls int
}

func (s *iteratingSessionStore) IterateSession(_ context.Context, _ string, _ func([]byte) error) (bool, error) {
	s.iterateCalls++
	return false, nil
}

func (s *iteratingSessionStore) IterateSessions(_ context.Context, _ []string, _ func(string, []byte) error) error {
	s.iterateCalls++
	return nil
}
func (*countingSessionStore) PutSession(context.Context, string, []byte) error           { return nil }
func (*countingSessionStore) PutManySessions(context.Context, map[string][]byte) error   { return nil }
func (*countingSessionStore) DeleteAllSessions(context.Context, string) error            { return nil }
func (*countingSessionStore) DeleteSession(context.Context, string) error                { return nil }
func (*countingSessionStore) MigratePNToLID(context.Context, types.JID, types.JID) error { return nil }

func TestContainsSessionUsesPrefetchedState(t *testing.T) {
	sessions := &countingSessionStore{}
	device := &Device{Sessions: sessions}
	address := protocol.NewSignalAddress("123", 1)
	_, ctx, err := device.WithCachedSessions(context.Background(), []string{address.String()})
	if err != nil {
		t.Fatal(err)
	}
	found, err := device.ContainsSession(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("unexpected cached session")
	}
	if sessions.hasCalls != 0 {
		t.Fatalf("expected no HasSession query, got %d", sessions.hasCalls)
	}
}

func TestCachedSessionsPreferEphemeralIterator(t *testing.T) {
	sessions := &iteratingSessionStore{}
	device := &Device{Sessions: sessions}
	address := protocol.NewSignalAddress("123", 1)
	_, _, err := device.WithCachedSessions(context.Background(), []string{address.String()})
	if err != nil {
		t.Fatal(err)
	}
	if sessions.iterateCalls != 1 {
		t.Fatalf("expected one iterator call, got %d", sessions.iterateCalls)
	}
	if sessions.getManyCalls != 0 {
		t.Fatalf("expected no copying GetManySessions call, got %d", sessions.getManyCalls)
	}
}

func TestLoadSessionPrefersEphemeralIterator(t *testing.T) {
	sessions := &iteratingSessionStore{}
	device := &Device{Sessions: sessions}
	address := protocol.NewSignalAddress("123", 1)
	loaded, err := device.LoadSession(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("missing empty session")
	}
	if sessions.iterateCalls != 1 {
		t.Fatalf("expected one iterator call, got %d", sessions.iterateCalls)
	}
	if sessions.getCalls != 0 {
		t.Fatalf("expected no copying GetSession call, got %d", sessions.getCalls)
	}
}
