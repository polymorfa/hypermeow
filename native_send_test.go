package whatsmeow

import (
	"context"
	"errors"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/types"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestNativeSendRejectsUnsupportedBeforePreparation(t *testing.T) {
	cli := &Client{}
	to := types.NewJID("15550000000", types.DefaultUserServer)
	hooks := &NativeSendHooks{Admit: func(context.Context, types.MessageID, func() error) error {
		t.Fatal("unexpected admission")
		return nil
	}, Entered: func() { t.Fatal("unexpected entry") }}
	for _, tc := range []struct {
		name    string
		message *waE2E.Message
		extra   SendRequestExtra
	}{
		{"missing ID", &waE2E.Message{Conversation: proto.String("text")}, SendRequestExtra{NativeSend: hooks}},
		{"media", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, SendRequestExtra{ID: "original", NativeSend: hooks}},
		{"sibling", &waE2E.Message{Conversation: proto.String("text"), ImageMessage: &waE2E.ImageMessage{}}, SendRequestExtra{ID: "original", NativeSend: hooks}},
		{"peer", &waE2E.Message{Conversation: proto.String("text")}, SendRequestExtra{ID: "original", NativeSend: hooks, Peer: true}},
		{"missing callback", &waE2E.Message{Conversation: proto.String("text")}, SendRequestExtra{ID: "original", NativeSend: &NativeSendHooks{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cli.SendMessage(t.Context(), to, tc.message, tc.extra)
			if !errors.Is(err, ErrNativeSendUnsupported) {
				t.Fatalf("wrong refusal: %v", err)
			}
		})
	}
}
