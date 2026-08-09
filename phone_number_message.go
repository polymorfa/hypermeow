package whatsmeow

import "go.mau.fi/whatsmeow/proto/waE2E"

func BuildRequestPhoneNumberMessage(contextInfo *waE2E.ContextInfo) *waE2E.Message {
	return &waE2E.Message{
		RequestPhoneNumberMessage: &waE2E.RequestPhoneNumberMessage{
			ContextInfo: contextInfo,
		},
	}
}

func BuildSharePhoneNumberMessage() *waE2E.Message {
	messageType := waE2E.ProtocolMessage_SHARE_PHONE_NUMBER
	return &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{Type: &messageType},
	}
}
