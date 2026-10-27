// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
)

func newsletterCounter(attrs *waBinary.AttrUtility) (int, bool) {
	value, ok := attrs.GetInt64("count", true)
	return int(value), ok && attrs.OK() && value >= 0 && int64(int(value)) == value
}

// Preserve wire observations separately from the legacy scalar count fields.
// Invalid or ambiguous counters are omitted, never turned into reported zeros.
func parseNewsletterObservations(msg *types.NewsletterMessage, children []waBinary.Node) bool {
	var badViews, badForwards, badReactions, sawForwards, sawReactions bool
	for _, child := range children {
		switch child.Tag {
		case "views_count":
			attrs := child.AttrGetter()
			definition := attrs.OptionalString("type")
			count, valid := newsletterCounter(attrs)
			msg.ViewsCount = count
			if !valid || len(msg.ViewCounts) == 2 {
				badViews = true
			}
			if !badViews {
				msg.ViewCounts = append(msg.ViewCounts, types.NewsletterViewCount{Type: definition, Count: count})
				msg.ViewsCountPresent = true
			}
		case "forwards_count":
			count, valid := newsletterCounter(child.AttrGetter())
			msg.ForwardsCount = count
			badForwards = badForwards || !valid || sawForwards
			sawForwards = true
			msg.ForwardsCountPresent = !badForwards
		case "reactions":
			badReactions = badReactions || sawReactions
			sawReactions = true
			counts := make(map[string]int)
			entries := 0
			for _, reaction := range child.GetChildren() {
				if reaction.Tag != "reaction" {
					continue
				}
				entries++
				if entries > 1000 {
					badReactions = true
					break
				}
				attrs := reaction.AttrGetter()
				code := attrs.String("code")
				count, valid := newsletterCounter(attrs)
				if !valid || code == "" {
					badReactions = true
					continue
				}
				counts[code] = count
			}
			msg.ReactionCounts = counts
		}
	}
	if badViews {
		msg.ViewCounts = nil
		msg.ViewsCountPresent = false
	}
	if badReactions {
		msg.ReactionCounts = nil
	}
	return !badViews && !badForwards && !badReactions
}
