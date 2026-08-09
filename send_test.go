package whatsmeow

import (
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
)

func TestButtonAndListResponsesDoNotRequestBusinessMetadata(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.Message
	}{
		{name: "buttons response", msg: &waE2E.Message{ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{}}},
		{name: "list response", msg: &waE2E.Message{ListResponseMessage: &waE2E.ListResponseMessage{}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := getButtonTypeFromMessage(tc.msg); got != "" {
				t.Fatalf("response requested unexpected business metadata type %q", got)
			}
		})
	}
}

func TestSetParticipantHashMismatch(t *testing.T) {
	tests := []struct {
		name string
		sent string
		ack  string
		want bool
	}{
		{name: "matching", sent: "same", ack: "same"},
		{name: "missing acknowledgement hash", sent: "sent"},
		{name: "mismatch", sent: "old", ack: "new", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := SendResponse{}
			if got := setParticipantHashMismatch(&resp, tc.sent, tc.ack); got != tc.want {
				t.Fatalf("mismatch = %t, want %t", got, tc.want)
			}
			if resp.PHashMismatch != tc.want {
				t.Fatalf("response mismatch = %t, want %t", resp.PHashMismatch, tc.want)
			}
		})
	}
}
