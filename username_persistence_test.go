package whatsmeow

import (
	"context"
	"testing"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type singleUsernameStore struct {
	store.NoopStore
	entries []store.ContactUsernameEntry
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
