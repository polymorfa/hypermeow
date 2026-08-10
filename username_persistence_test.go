package whatsmeow

import (
	"context"
	"errors"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

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
