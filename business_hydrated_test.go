package whatsmeow

import (
	"bytes"
	"fmt"
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/proto/waE2E"
)

// TestHydratedMixedButtonsRoundTrip checks stable reply IDs, one-based button
// indexes and quote context after encoding, plus invalid builder inputs.
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

// TestHydratedButtonLimitsIndependently uses distinct reply IDs to isolate the
// ten-control and three-CTA ceilings from duplicate-ID validation failures.
func TestHydratedButtonLimitsIndependently(t *testing.T) {
	reply := func(i int) BusinessNativeFlowButton {
		return BusinessNativeFlowButton{Name: "quick_reply", ParamsJSON: fmt.Sprintf(`{"display_text":"Reply","id":"reply-%d"}`, i)}
	}
	url := BusinessNativeFlowButton{Name: "cta_url", ParamsJSON: `{"display_text":"Open","url":"https://example.com"}`}
	var eleven []BusinessNativeFlowButton
	for i := 0; i < 11; i++ {
		eleven = append(eleven, reply(i))
	}
	t.Run("ten controls including three CTAs", func(t *testing.T) {
		buttons := append(append([]BusinessNativeFlowButton{}, eleven[:7]...), url, url, url)
		message, err := BuildBusinessHydratedButtonsMessage(BusinessHydratedButtonsMessageParams{Body: "Body", Buttons: buttons})
		if err != nil {
			t.Fatal(err)
		}
		if len(message.GetTemplateMessage().GetHydratedTemplate().GetHydratedButtons()) != 10 {
			t.Fatal("valid boundary did not retain all ten controls")
		}
	})
	t.Run("eleven unique reply IDs", func(t *testing.T) {
		if _, err := BuildBusinessHydratedButtonsMessage(BusinessHydratedButtonsMessageParams{Body: "Body", Buttons: eleven}); err == nil {
			t.Fatal("eleven controls accepted")
		}
	})
	t.Run("four valid CTAs", func(t *testing.T) {
		if _, err := BuildBusinessHydratedButtonsMessage(BusinessHydratedButtonsMessageParams{Body: "Body", Buttons: []BusinessNativeFlowButton{url, url, url, url}}); err == nil {
			t.Fatal("four CTAs accepted")
		}
	})
}

// TestHydratedRelayEnvelopePreservesCiphertextWithoutApprovalClaims requires empty
// biz/hsm format nodes beside unchanged encrypted bytes, without approval attributes.
func TestHydratedRelayEnvelopePreservesCiphertextWithoutApprovalClaims(t *testing.T) {
	message, err := BuildBusinessHydratedButtonsMessage(BusinessHydratedButtonsMessageParams{Body: "Offer", Buttons: []BusinessNativeFlowButton{{Name: "quick_reply", ParamsJSON: `{"display_text":"Reply","id":"stable"}`}, {Name: "cta_url", ParamsJSON: `{"display_text":"Open","url":"https://example.com"}`}}})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := []byte("opaque encrypted payload")
	content := (&Client{}).getMessageContent(waBinary.Node{Tag: "enc", Content: ciphertext}, message, nil, false, nodeExtraParams{})
	if len(content) != 3 || content[0].Tag != "enc" || content[1].Tag != "biz" || content[2].Tag != "hsm" {
		t.Fatal("missing root format envelope")
	}
	if !bytes.Equal(content[0].Content.([]byte), ciphertext) {
		t.Fatal("ciphertext changed")
	}
	for _, node := range content[1:] {
		if len(node.Attrs) != 0 || node.Content != nil {
			t.Fatal("format metadata asserted provider approval or identity")
		}
	}
}
