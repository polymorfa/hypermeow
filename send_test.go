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
