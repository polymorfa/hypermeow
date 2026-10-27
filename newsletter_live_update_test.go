// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
)

func TestNewsletterLiveUpdatesPreserveWrappedAndLegacyMessages(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		name := "legacy"
		if wrapped {
			name = "wrapped"
		}
		t.Run(name, func(t *testing.T) {
			cli, _, _ := newTestRemote(t)
			var got *events.NewsletterLiveUpdate
			cli.AddEventHandler(func(evt any) {
				got, _ = evt.(*events.NewsletterLiveUpdate)
			})
			payload, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("synthetic channel update")})
			if err != nil {
				t.Fatal(err)
			}
			children := []waBinary.Node{{
				Tag: "message", Attrs: waBinary.Attrs{"server_id": "7", "id": "synthetic-message", "type": "text", "t": "1700000000"},
				Content: []waBinary.Node{
					{Tag: "plaintext", Content: payload},
					{Tag: "views_count", Attrs: waBinary.Attrs{"count": "11"}},
					{Tag: "forwards_count", Attrs: waBinary.Attrs{"count": "3"}},
				},
			}}
			if wrapped {
				children = []waBinary.Node{{Tag: "messages", Content: children}}
			}
			jid := types.NewJID("120363000000001", types.NewsletterServer)
			cli.handleNewsletterNotification(context.Background(), &waBinary.Node{
				Tag: "notification", Attrs: waBinary.Attrs{"from": jid, "t": "1700000000"},
				Content: []waBinary.Node{{Tag: "live_updates", Content: children}},
			})
			if got == nil || got.JID != jid || len(got.Messages) != 1 {
				t.Fatalf("live update lost its message: %+v", got)
			}
			message := got.Messages[0]
			if message.MessageServerID != 7 || message.ViewsCount != 11 || message.ForwardsCount != 3 || message.Message.GetConversation() != "synthetic channel update" {
				t.Fatalf("live update lost content or counts: %+v", message)
			}
		})
	}
}
