package whatsmeow

import (
	"context"
	"testing"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
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
