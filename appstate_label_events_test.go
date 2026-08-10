package whatsmeow

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/appstate"
	"github.com/polymorfa/hypermeow/proto/waServerSync"
	"github.com/polymorfa/hypermeow/proto/waSyncAction"
	"github.com/polymorfa/hypermeow/types/events"
)

func TestSelectiveFullSyncLabelEvents(t *testing.T) {
	client := &Client{EmitLabelEventsOnFullSync: true}
	for _, test := range []struct {
		index string
		want  bool
	}{
		{appstate.IndexLabelEdit, true},
		{appstate.IndexLabelAssociationChat, true},
		{appstate.IndexLabelAssociationMessage, true},
		{appstate.IndexMute, false},
		{"", false},
	} {
		if got := client.shouldEmitFullSyncMutation([]string{test.index}); got != test.want {
			t.Fatalf("index %q: got %v, want %v", test.index, got, test.want)
		}
	}
	client.EmitAppStateEventsOnFullSync = true
	if !client.shouldEmitFullSyncMutation([]string{appstate.IndexMute}) {
		t.Fatal("full event mode did not emit non-label mutation")
	}
	client.EmitAppStateEventsOnFullSync = false
	client.EmitQuickReplyEventsOnFullSync = true
	if !client.shouldEmitFullSyncMutation([]string{appstate.IndexQuickReply}) {
		t.Fatal("quick reply event mode did not emit quick reply mutation")
	}
	if client.shouldEmitFullSyncMutation([]string{appstate.IndexMute}) {
		t.Fatal("quick reply event mode emitted unrelated mutation")
	}
}

func TestQuickReplyAppStateEvent(t *testing.T) {
	client := &Client{}
	timestamp := int64(1700000000000)
	got := client.dispatchAppState(context.Background(), appstate.WAPatchRegular, appstate.Mutation{
		Operation: waServerSync.SyncdMutation_SET,
		Index:     []string{appstate.IndexQuickReply, "1700000000"},
		Action: &waSyncAction.SyncActionValue{
			Timestamp: proto.Int64(timestamp),
			QuickReplyAction: &waSyncAction.QuickReplyAction{
				Shortcut: proto.String("hours"),
				Message:  proto.String("We are open until 18:00."),
			},
		},
	}, true)
	event, ok := got.(*events.QuickReply)
	if !ok {
		t.Fatalf("event = %T, want *events.QuickReply", got)
	}
	if event.ID != "1700000000" || event.Timestamp != time.UnixMilli(timestamp) || !event.FromFullSync {
		t.Fatalf("event = %#v", event)
	}
	if event.Action.GetShortcut() != "hours" || event.Action.GetMessage() != "We are open until 18:00." {
		t.Fatalf("action = %#v", event.Action)
	}
}
