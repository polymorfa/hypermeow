package whatsmeow

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/proto/waE2E"
)

func TestHydratedMixedButtonsRoundTrip(t *testing.T) {
	params := BusinessHydratedButtonsMessageParams{Body: "Mixed fixture", Footer: "Footer", ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("quoted-fixture")}, Buttons: []BusinessNativeFlowButton{
		{Name: "quick_reply", ParamsJSON: `{"display_text":"Reply","id":"stable-reply"}`},
		{Name: "cta_url", ParamsJSON: `{"display_text":"Open","url":"https://example.com"}`},
		{Name: "cta_call", ParamsJSON: `{"display_text":"Call","phone_number":"+12025550123"}`},
	}}
	message, err := BuildBusinessHydratedButtonsMessage(params)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &waE2E.Message{}
	if err = proto.Unmarshal(wire, decoded); err != nil {
		t.Fatal(err)
	}
	if getButtonTypeFromMessage(decoded) != "template" {
		t.Fatal("missing template format classification")
	}
	template := decoded.GetTemplateMessage().GetHydratedTemplate()
	if len(template.GetHydratedButtons()) != 3 || template.GetHydratedButtons()[0].GetQuickReplyButton().GetID() != "stable-reply" || decoded.GetTemplateMessage().GetContextInfo().GetStanzaID() != "quoted-fixture" {
		t.Fatal("mixed buttons or quote context lost")
	}
	for i, button := range template.GetHydratedButtons() {
		if button.GetIndex() != uint32(i+1) {
			t.Fatal("button index lost")
		}
	}
	for _, bad := range []BusinessHydratedButtonsMessageParams{
		{Body: "Body"},
		{Body: "Body", Buttons: append(params.Buttons, params.Buttons[0])},
		{Body: "Body", Buttons: []BusinessNativeFlowButton{{Name: "cta_copy", ParamsJSON: `{"display_text":"Copy","copy_code":"CODE"}`}}},
	} {
		if _, err := BuildBusinessHydratedButtonsMessage(bad); err == nil {
			t.Fatal("invalid hydrated button message accepted")
		}
	}
}
