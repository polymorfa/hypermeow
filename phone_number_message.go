// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import "github.com/polymorfa/hypermeow/proto/waE2E"

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
