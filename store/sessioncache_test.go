package store

import (
	"context"
	"testing"

	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/whatsmeow/types"
)

type countingSessionStore struct {
	hasCalls int
}

func (*countingSessionStore) GetSession(context.Context, string) ([]byte, error) { return nil, nil }
func (s *countingSessionStore) HasSession(context.Context, string) (bool, error) {
	s.hasCalls++
	return false, nil
}
func (*countingSessionStore) GetManySessions(_ context.Context, addresses []string) (map[string][]byte, error) {
	result := make(map[string][]byte, len(addresses))
	for _, address := range addresses {
		result[address] = nil
	}
	return result, nil
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
