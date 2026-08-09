package whatsmeow

import (
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
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

func TestInteractiveNativeFlowsRequestNamedBusinessMetadata(t *testing.T) {
	for _, name := range []string{"address_message", "galaxy_message"} {
		t.Run(name, func(t *testing.T) {
			msg := &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
				InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
					Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{{Name: proto.String(name)}},
				}},
			}}
			if got := getButtonTypeFromMessage(msg); got != "native_flow" {
				t.Fatalf("button type = %q", got)
			}
			biz := buildNativeFlowBizNode(msg, 1_700_000_000)
			if biz.Tag != "biz" || biz.Attrs["actual_actors"] != "2" || biz.Attrs["host_storage"] != "2" || biz.Attrs["privacy_mode_ts"] != "1700000000" {
				t.Fatalf("unexpected biz attrs: %#v", biz)
			}
			children, ok := biz.Content.([]waBinary.Node)
			if !ok || len(children) != 2 || children[0].Tag != "interactive" {
				t.Fatalf("unexpected biz children: %#v", biz.Content)
			}
			flowChildren := children[0].Content.([]waBinary.Node)
			if flowChildren[0].Tag != "native_flow" || flowChildren[0].Attrs["name"] != name || flowChildren[0].Attrs["v"] != "9" {
				t.Fatalf("unexpected native-flow metadata: %#v", flowChildren[0])
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
