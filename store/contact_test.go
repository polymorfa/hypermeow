package store

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestContactEntryMassInsertIncludesUsername(t *testing.T) {
	entry := ContactEntry{
		JID:       types.NewJID("100000011111111", types.HiddenUserServer),
		FirstName: "Example",
		FullName:  "Example User",
		Username:  "example",
	}
	values := entry.GetMassInsertValues()
	if values[3] != "example" {
		t.Fatalf("username value = %#v", values[3])
	}
}
