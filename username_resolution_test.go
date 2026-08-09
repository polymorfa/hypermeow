package whatsmeow

import (
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
)

func TestParseUsernameResolution(t *testing.T) {
	list := &waBinary.Node{Tag: "list", Content: []waBinary.Node{{
		Tag:   "user",
		Attrs: waBinary.Attrs{"jid": types.NewJID("100000011111111", types.HiddenUserServer)},
		Content: []waBinary.Node{{
			Tag:   "contact",
			Attrs: waBinary.Attrs{"type": "in", "username": "example"},
		}},
	}}}
	result, err := parseUsernameResolution(list)
	if err != nil {
		t.Fatal(err)
	}
	if result.LID.String() != "100000011111111@lid" || result.Username != "example" || result.KeyRequired {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestParseUsernameResolutionDetectsRequiredKey(t *testing.T) {
	list := &waBinary.Node{Tag: "list", Content: []waBinary.Node{{
		Tag: "user",
		Content: []waBinary.Node{{
			Tag:   "contact",
			Attrs: waBinary.Attrs{"type": "in"},
		}},
	}}}
	result, err := parseUsernameResolution(list)
	if err != nil {
		t.Fatal(err)
	}
	if !result.KeyRequired {
		t.Fatal("expected username key requirement")
	}
}

func TestHistoricalInlineContactsPreferLID(t *testing.T) {
	entries, mappings := historicalInlineContactEntries([]*waHistorySync.InlineContact{{
		PnJID:    stringPtr("15550001111@s.whatsapp.net"),
		LidJID:   stringPtr("100000011111111@lid"),
		FullName: stringPtr("Example User"),
		Username: stringPtr("example"),
	}})
	if len(entries) != 1 || entries[0].JID.String() != "100000011111111@lid" || entries[0].Username != "example" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	if len(mappings) != 1 || mappings[0].LID != entries[0].JID {
		t.Fatalf("unexpected mappings: %+v", mappings)
	}
}
