package whatsmeow

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestFilterContactsPreservesUsername(t *testing.T) {
	client := &Client{}
	_, contacts := client.filterContacts([]appstate.Mutation{
		{
			Index: []string{appstate.IndexContact, "100000011111111@lid"},
			Action: &waSyncAction.SyncActionValue{ContactAction: &waSyncAction.ContactAction{
				FullName: proto.String("Example User"),
				Username: proto.String("example"),
			}},
		},
		{
			Index: []string{appstate.IndexLIDContact, "100000022222222@lid"},
			Action: &waSyncAction.SyncActionValue{LidContactAction: &waSyncAction.LidContactAction{
				FullName: proto.String("LID User"),
				Username: proto.String("lid-example"),
			}},
		},
	})
	if len(contacts) != 2 {
		t.Fatalf("got %d contacts", len(contacts))
	}
	if contacts[0].Username != "example" || contacts[1].Username != "lid-example" {
		t.Fatalf("usernames = %q, %q", contacts[0].Username, contacts[1].Username)
	}
}

type recordingLIDContactStore struct {
	store.NoopStore
	jid      types.JID
	fullName string
	username string
}

func (contacts *recordingLIDContactStore) PutContactName(_ context.Context, jid types.JID, _, fullName string) error {
	contacts.jid = jid
	contacts.fullName = fullName
	return nil
}

func (contacts *recordingLIDContactStore) PutContactUsername(_ context.Context, jid types.JID, username string) error {
	contacts.jid = jid
	contacts.username = username
	return nil
}

func TestDispatchLIDContactPersistsNamesAndUsername(t *testing.T) {
	contacts := &recordingLIDContactStore{}
	client := &Client{Store: &store.Device{Contacts: contacts}}
	lid := types.NewJID("100000011111111", types.HiddenUserServer)
	event := client.dispatchAppState(context.Background(), appstate.WAPatchCriticalUnblockLow, appstate.Mutation{
		Index: []string{appstate.IndexLIDContact, lid.String()},
		Action: &waSyncAction.SyncActionValue{LidContactAction: &waSyncAction.LidContactAction{
			FullName: proto.String("LID User"), Username: proto.String("lid-example"),
		}},
	}, false)
	if contacts.jid != lid || contacts.fullName != "LID User" || contacts.username != "lid-example" {
		t.Fatalf("unexpected persisted contact: %#v", contacts)
	}
	lidEvent, ok := event.(*events.LIDContact)
	if !ok || lidEvent.JID != lid || lidEvent.Action.GetUsername() != "lid-example" {
		t.Fatalf("unexpected LID contact event: %#v", event)
	}
}
