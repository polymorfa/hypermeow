// Copyright (c) 2026 Rajeh Taher
// Copyright (c) 2026 Polymorfa (Softium LLC)
// Licensed under the MIT License. See LICENSE-MIT for details.

package events

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestNewsletterMilestoneAcceptsQuotedAndNumericPositions(t *testing.T) {
	for _, position := range []string{`"123"`, `123`} {
		t.Run(position, func(t *testing.T) {
			var event NewsletterMilestone
			err := json.Unmarshal([]byte(`{"newsletter_id":"123@newsletter","message_server_id":`+position+`,"type":"MESSAGE_FORWARDS","value":0,"reaction_code":""}`), &event)
			if err != nil {
				t.Fatal(err)
			}
			if event.MessageServerID != 123 || event.ID.String() != "123@newsletter" || event.Type != "MESSAGE_FORWARDS" || event.Value != 0 {
				t.Fatalf("milestone lost its identity or fields: %+v", event)
			}
		})
	}
}

func TestNewsletterMilestoneRejectsInvalidPositions(t *testing.T) {
	for _, position := range []string{`true`, `{}`, `[]`, `""`, `-1`, `"-1"`, `1.5`, `"1.5"`, `"+123"`, `" 123"`, `9223372036854775808`} {
		t.Run(position, func(t *testing.T) {
			var event NewsletterMilestone
			if err := json.Unmarshal([]byte(`{"message_server_id":`+position+`}`), &event); err == nil {
				t.Fatal("invalid server position was accepted")
			}
		})
	}
}

func TestNewsletterMilestoneKeepsIntegerPrecisionAndUnknownType(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("position exceeds 32-bit native representation")
	}
	var event NewsletterMilestone
	if err := json.Unmarshal([]byte(`{"message_server_id":9007199254740993,"type":"FUTURE_MILESTONE","value":17}`), &event); err != nil {
		t.Fatal(err)
	}
	if int64(event.MessageServerID) != 9007199254740993 || event.Type != "FUTURE_MILESTONE" || event.Value != 17 {
		t.Fatalf("integer position or unknown type was changed: %+v", event)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["message_server_id"]) != `"9007199254740993"` {
		t.Fatal("canonical quoted ID serialization changed")
	}
}

func TestNewsletterMilestoneWithoutMessageHasNoPosition(t *testing.T) {
	for _, body := range []string{
		`{"newsletter_id":"123@newsletter","type":"FOLLOWS","value":1000}`,
		`{"newsletter_id":"123@newsletter","message_server_id":null,"type":"FOLLOWS","value":1000}`,
		`{"newsletter_id":"123@newsletter","message_server_id":0,"type":"FOLLOWS","value":1000}`,
		`{"newsletter_id":"123@newsletter","message_server_id":"0","type":"FOLLOWS","value":1000}`,
	} {
		var event NewsletterMilestone
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			t.Fatalf("%s: channel-level milestone was rejected: %v", body, err)
		}
		if event.MessageServerID != 0 || event.Type != "FOLLOWS" || event.Value != 1000 {
			t.Fatalf("%s: decoded %+v", body, event)
		}
	}
}
