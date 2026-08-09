package whatsmeow

import (
	"encoding/json"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestBuildDeleteNewsletterVariablesRejectsNonNewsletterJID(t *testing.T) {
	tests := []types.JID{
		types.EmptyJID,
		types.NewJID("15551234567", types.DefaultUserServer),
		types.NewJID("120363000000000000", types.GroupServer),
	}
	for _, jid := range tests {
		if _, err := buildDeleteNewsletterVariables(jid); err == nil {
			t.Errorf("expected %q to be rejected", jid)
		}
	}
}

func TestBuildDeleteNewsletterVariablesUsesCanonicalJID(t *testing.T) {
	jid := types.NewJID("120363000000000001", types.NewsletterServer)
	got, err := buildDeleteNewsletterVariables(jid)
	if err != nil {
		t.Fatal(err)
	}
	if got.NewsletterID != "120363000000000001@newsletter" {
		t.Fatalf("newsletter_id = %q", got.NewsletterID)
	}
}

func TestDecodeDeleteNewsletterResponseRequiresMatchingDeletedState(t *testing.T) {
	want := types.NewJID("120363000000000001", types.NewsletterServer)
	tests := []struct {
		name string
		raw  string
	}{
		{"missing discriminator", `{"unexpected":{}}`},
		{"null result", `{"xwa2_newsletter_delete_v2":null}`},
		{"wrong id", `{"xwa2_newsletter_delete_v2":{"id":"120363000000000002@newsletter","state":{"type":"DELETED"}}}`},
		{"active state", `{"xwa2_newsletter_delete_v2":{"id":"120363000000000001@newsletter","state":{"type":"ACTIVE"}}}`},
		{"missing state", `{"xwa2_newsletter_delete_v2":{"id":"120363000000000001@newsletter"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := decodeDeleteNewsletterResponse(json.RawMessage(tc.raw), want); err == nil {
				t.Fatal("expected response validation error")
			}
		})
	}
}

func TestDecodeDeleteNewsletterResponseAcceptsMatchingDeletedState(t *testing.T) {
	want := types.NewJID("120363000000000001", types.NewsletterServer)
	raw := json.RawMessage(`{"xwa2_newsletter_delete_v2":{"id":"120363000000000001@newsletter","state":{"type":"DELETED"}}}`)
	if err := decodeDeleteNewsletterResponse(raw, want); err != nil {
		t.Fatal(err)
	}
}
