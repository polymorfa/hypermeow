package whatsmeow

import (
	"testing"

	"go.mau.fi/whatsmeow/appstate"
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
}
