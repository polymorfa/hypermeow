package whatsmeow

import (
	"context"
	"errors"
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
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

func TestParseGroupChangeReturnsParticipantUsernames(t *testing.T) {
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	node := &waBinary.Node{
		Tag:   "notification",
		Attrs: waBinary.Attrs{"from": types.NewJID("120363000000000000", types.GroupServer), "t": "1"},
		Content: []waBinary.Node{{
			Tag: "add",
			Content: []waBinary.Node{{
				Tag:   "participant",
				Attrs: waBinary.Attrs{"jid": lid, "username": "example"},
			}},
		}},
	}
	_, _, usernames, err := (&Client{}).parseGroupChangeWithUsernames(node)
	if err != nil {
		t.Fatal(err)
	}
	if len(usernames) != 1 || usernames[0].JID != lid || usernames[0].Username != "example" {
		t.Fatalf("group-change usernames = %#v", usernames)
	}
}

func TestParseGroupParticipantRequestsReturnsUsernamesByStableLID(t *testing.T) {
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	pn := types.NewJID("15550001111", types.DefaultUserServer)
	nodes := []waBinary.Node{
		{Tag: "membership_approval_request", Attrs: waBinary.Attrs{
			"jid": lid, "username": "lid-user", "request_time": "1",
		}},
		{Tag: "membership_approval_request", Attrs: waBinary.Attrs{
			"jid": pn, "lid": lid, "username": "pn-user", "request_time": "2",
		}},
	}

	requests, usernames := parseGroupParticipantRequests(nodes)
	if len(requests) != 2 || requests[0].JID != lid || requests[1].JID != pn {
		t.Fatalf("participant requests = %#v", requests)
	}
	if len(usernames) != 2 {
		t.Fatalf("usernames = %#v", usernames)
	}
	if usernames[0].JID != lid || usernames[0].Username != "lid-user" {
		t.Fatalf("LID-addressed username = %#v", usernames[0])
	}
	if usernames[1].JID != lid || usernames[1].Username != "pn-user" {
		t.Fatalf("PN-addressed username = %#v", usernames[1])
	}
}
