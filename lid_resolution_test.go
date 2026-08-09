package whatsmeow

import (
	"context"
	"testing"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

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
	pn := types.NewJID("15550001111", types.DefaultUserServer)
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	lids := &cachedLIDStore{pn: pn, lid: lid}
	client := NewClient(&store.Device{LIDs: lids}, waLog.Noop)

	resolved, err := client.resolveLID(context.Background(), pn)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != lid {
		t.Fatalf("resolved LID = %s, want %s", resolved, lid)
	}
}

func TestResolveLIDRejectsNonPhoneJID(t *testing.T) {
	client := NewClient(&store.Device{LIDs: &store.NoopStore{}}, waLog.Noop)
	if _, err := client.resolveLID(context.Background(), types.NewJID("100000011111111", types.HiddenUserServer)); err == nil {
		t.Fatal("expected non-PN JID to fail")
	}
}
