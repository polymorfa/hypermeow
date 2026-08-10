package whatsmeow

import (
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func TestParseGroupParticipantPreservesUsername(t *testing.T) {
	node := &waBinary.Node{Tag: "participant", Attrs: waBinary.Attrs{
		"jid":      types.NewJID("100000011111111", types.HiddenUserServer),
		"username": "example",
	}}
	ag := node.AttrGetter()
	participant := parseParticipant(ag, node)
	if participant.Username != "example" {
		t.Fatalf("username = %q", participant.Username)
	}
}
