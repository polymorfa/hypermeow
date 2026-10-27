// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"fmt"
	"slices"
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
)

func TestNewsletterObservationsPreserveReportedZeroAndMissingCounts(t *testing.T) {
	for _, reported := range []bool{false, true} {
		cli, _, _ := newTestRemote(t)
		var observed *events.NewsletterLiveUpdate
		cli.AddEventHandler(func(evt any) { observed, _ = evt.(*events.NewsletterLiveUpdate) })
		var counters []waBinary.Node
		if reported {
			counters = []waBinary.Node{
				{Tag: "views_count", Attrs: waBinary.Attrs{"count": "0", "type": "views"}},
				{Tag: "forwards_count", Attrs: waBinary.Attrs{"count": "0"}},
				{Tag: "reactions"},
			}
		}
		cli.handleNewsletterNotification(context.Background(), &waBinary.Node{
			Tag: "notification", Attrs: waBinary.Attrs{"from": types.NewJID("123", types.NewsletterServer)},
			Content: []waBinary.Node{{Tag: "live_updates", Content: []waBinary.Node{{Tag: "messages", Content: []waBinary.Node{
				{Tag: "message", Attrs: waBinary.Attrs{"server_id": "123"}, Content: counters},
			}}}}},
		})
		if observed == nil || len(observed.Messages) != 1 || observed.Messages[0].MessageID != "" {
			t.Fatal("position-only observation was discarded or invented an original ID")
		}
		message := observed.Messages[0]
		if message.ViewsCountPresent != reported || message.ForwardsCountPresent != reported || (message.ReactionCounts != nil) != reported {
			t.Fatalf("reported=%v lost count presence: %+v", reported, message)
		}
		if reported && !slices.Equal(message.ViewCounts, []types.NewsletterViewCount{{Type: "views", Count: 0}}) {
			t.Fatalf("reported zero lost its view definition: %+v", message.ViewCounts)
		}
	}
}

func TestNewsletterObservationsRetainBothViewDefinitionsInEitherOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		cli, _, _ := newTestRemote(t)
		definitions := []waBinary.Node{
			{Tag: "views_count", Attrs: waBinary.Attrs{"count": "7"}},
			{Tag: "views_count", Attrs: waBinary.Attrs{"count": "11", "type": "views"}},
		}
		expected := []types.NewsletterViewCount{{Count: 7}, {Type: "views", Count: 11}}
		if reverse {
			slices.Reverse(definitions)
			slices.Reverse(expected)
		}
		messages := cli.parseNewsletterMessages(&waBinary.Node{Content: []waBinary.Node{{Tag: "message", Attrs: waBinary.Attrs{"server_id": "123"}, Content: definitions}}})
		if len(messages) != 1 || !messages[0].ViewsCountPresent || !slices.Equal(messages[0].ViewCounts, expected) {
			t.Fatalf("view definitions were collapsed or reordered: %+v", messages)
		}
	}
}

func TestNewsletterObservationsDoNotFabricateMalformedCounts(t *testing.T) {
	for _, counters := range [][]waBinary.Node{
		{{Tag: "views_count", Attrs: waBinary.Attrs{"count": "-1"}}, {Tag: "forwards_count", Attrs: waBinary.Attrs{"count": "invalid"}}},
		{{Tag: "views_count", Attrs: waBinary.Attrs{"count": "1"}}, {Tag: "views_count", Attrs: waBinary.Attrs{"count": "2"}}, {Tag: "views_count", Attrs: waBinary.Attrs{"count": "3"}}},
		{{Tag: "forwards_count", Attrs: waBinary.Attrs{"count": "1"}}, {Tag: "forwards_count", Attrs: waBinary.Attrs{"count": "2"}}},
		{{Tag: "views_count", Attrs: waBinary.Attrs{"count": "9223372036854775808"}}},
		{{Tag: "reactions", Content: []waBinary.Node{{Tag: "reaction", Attrs: waBinary.Attrs{"code": "x", "count": "bad"}}}}},
	} {
		cli, _, _ := newTestRemote(t)
		messages := cli.parseNewsletterMessages(&waBinary.Node{Content: []waBinary.Node{{Tag: "message", Attrs: waBinary.Attrs{"server_id": "123"}, Content: counters}}})
		if len(messages) != 1 || messages[0].ViewsCountPresent || messages[0].ForwardsCountPresent || messages[0].ViewCounts != nil || messages[0].ReactionCounts != nil {
			t.Fatalf("malformed count became an observation: %+v", messages)
		}
	}
}

func TestNewsletterReactionLimitDoesNotSilentlyTruncate(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	for _, count := range []int{1000, 1001} {
		reactions := make([]waBinary.Node, count)
		for i := range reactions {
			reactions[i] = waBinary.Node{Tag: "reaction", Attrs: waBinary.Attrs{"code": fmt.Sprint(i), "count": "1"}}
		}
		messages := cli.parseNewsletterMessages(&waBinary.Node{Content: []waBinary.Node{{Tag: "message", Attrs: waBinary.Attrs{"server_id": "123"}, Content: []waBinary.Node{{Tag: "reactions", Content: reactions}}}}})
		if count == 1000 && len(messages[0].ReactionCounts) != 1000 || count == 1001 && messages[0].ReactionCounts != nil {
			t.Fatal("reaction bound dropped a valid list or exposed a truncated list")
		}
	}
}

func TestNewsletterMilestoneNativeMexProducer(t *testing.T) {
	for _, position := range []string{`"123"`, `123`} {
		cli, _, _ := newTestRemote(t)
		var got *events.NewsletterMilestone
		cli.AddEventHandler(func(evt any) { got, _ = evt.(*events.NewsletterMilestone) })
		cli.handleMexNotification(context.Background(), &waBinary.Node{
			Tag: "notification", Attrs: waBinary.Attrs{"t": "1700000000"}, Content: []waBinary.Node{{
				Tag: "update", Attrs: waBinary.Attrs{"op_name": "NotificationNewsletterMilestone"},
				Content: []byte(`{"data":{"xwa2_notify_newsletter_milestone":{"newsletter_id":"123@newsletter","message_server_id":` + position + `,"type":"MESSAGE_FORWARDS","value":0}}}`),
			}},
		})
		if got == nil || got.MessageServerID != 123 || got.Mex.OpName != "NotificationNewsletterMilestone" || got.Mex.Timestamp.Unix() != 1700000000 {
			t.Fatalf("native milestone delivery lost identity or source context: %+v", got)
		}
	}
}
