package whatsmeow

import (
	"testing"

	"github.com/polymorfa/hypermeow/proto/waE2E"
)

func TestBuildRequestPhoneNumberMessage(t *testing.T) {
	contextInfo := &waE2E.ContextInfo{StanzaID: stringPtr("request-id")}
	message := BuildRequestPhoneNumberMessage(contextInfo)

	request := message.GetRequestPhoneNumberMessage()
	if request == nil {
		t.Fatal("expected request phone number message")
	}
	if request.GetContextInfo().GetStanzaID() != "request-id" {
		t.Fatalf("unexpected context info: %+v", request.GetContextInfo())
	}
}

func TestBuildSharePhoneNumberMessage(t *testing.T) {
	message := BuildSharePhoneNumberMessage()
	protocolMessage := message.GetProtocolMessage()
	if protocolMessage == nil {
		t.Fatal("expected protocol message")
	}
	if protocolMessage.GetType() != waE2E.ProtocolMessage_SHARE_PHONE_NUMBER {
		t.Fatalf("unexpected protocol message type: %s", protocolMessage.GetType())
	}
}

func stringPtr(value string) *string {
	return &value
}
