package whatsmeow

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
)

func TestFilterContactsPreservesUsername(t *testing.T) {
	client := &Client{}
	_, contacts := client.filterContacts([]appstate.Mutation{{
		Index: []string{"contact", "100000011111111@lid"},
		Action: &waSyncAction.SyncActionValue{ContactAction: &waSyncAction.ContactAction{
			FullName: proto.String("Example User"),
			Username: proto.String("example"),
		}},
	}})
	if len(contacts) != 1 {
		t.Fatalf("got %d contacts", len(contacts))
	}
	if contacts[0].Username != "example" {
		t.Fatalf("username = %q", contacts[0].Username)
	}
}
