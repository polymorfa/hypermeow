package whatsmeow

import (
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
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
