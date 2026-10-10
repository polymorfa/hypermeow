package whatsmeow

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/polymorfa/hypermeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func carouselCard(index int, video bool) BusinessCarouselCard {
	card := BusinessCarouselCard{Body: fmt.Sprintf("Card %d", index), Buttons: []BusinessNativeFlowButton{{Name: "quick_reply", ParamsJSON: fmt.Sprintf(`{"display_text":"Choose","id":"card-%d"}`, index)}}}
	if video {
		card.Video = &waE2E.VideoMessage{DirectPath: proto.String("/mms/video/synthetic"), Mimetype: proto.String("video/mp4"), MediaKey: bytes.Repeat([]byte{1}, 32), FileSHA256: bytes.Repeat([]byte{2}, 32), FileEncSHA256: bytes.Repeat([]byte{3}, 32)}
	} else {
		card.Image = &waE2E.ImageMessage{DirectPath: proto.String("/mms/image/synthetic"), Mimetype: proto.String("image/jpeg"), MediaKey: bytes.Repeat([]byte{1}, 32), FileSHA256: bytes.Repeat([]byte{2}, 32), FileEncSHA256: bytes.Repeat([]byte{3}, 32)}
	}
	return card
}

func TestBusinessCarouselRoundTripAndRelayMetadata(t *testing.T) {
	for _, count := range []int{2, 10} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			cards := make([]BusinessCarouselCard, count)
			for i := range cards {
				cards[i] = carouselCard(i, i%2 == 1)
			}
			context := &waE2E.ContextInfo{StanzaID: proto.String("quoted-synthetic")}
			msg, err := BuildBusinessCarouselMessage(BusinessCarouselMessageParams{Body: "Offers", Cards: cards, ContextInfo: context})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := proto.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			var decoded waE2E.Message
			if err = proto.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			interactive := decoded.GetInteractiveMessage()
			carousel := interactive.GetCarouselMessage()
			if len(carousel.GetCards()) != count || carousel.GetMessageVersion() != 1 || interactive.GetContextInfo().GetStanzaID() != "quoted-synthetic" {
				t.Fatal("carousel/context did not survive protobuf round trip")
			}
			for i, card := range carousel.GetCards() {
				if card.GetNativeFlowMessage().GetMessageVersion() != 1 || !card.GetHeader().GetHasMediaAttachment() {
					t.Fatalf("card%d lacks canonical native media header", i)
				}
				if i%2 == 0 && !proto.Equal(card.GetHeader().GetImageMessage(), cards[i].Image) {
					t.Fatal("image media mutated")
				}
				if i%2 == 1 && !proto.Equal(card.GetHeader().GetVideoMessage(), cards[i].Video) {
					t.Fatal("video media mutated")
				}
			}
			if getButtonTypeFromMessage(&decoded) != "native_flow" {
				t.Fatal("carousel must request native business metadata")
			}
			biz := buildNativeFlowBizNode(&decoded, 1)
			if biz.GetChildren()[0].GetChildren()[0].Attrs["name"] != "mixed" {
				t.Fatal("carousel requires mixed metadata for card controls")
			}
		})
	}
}

func TestBusinessCarouselRejectsInvalidCards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*BusinessCarouselMessageParams)
	}{
		{"one", func(p *BusinessCarouselMessageParams) { p.Cards = p.Cards[:1] }},
		{"eleven", func(p *BusinessCarouselMessageParams) {
			for len(p.Cards) < 11 {
				p.Cards = append(p.Cards, carouselCard(len(p.Cards), false))
			}
		}},
		{"missing media", func(p *BusinessCarouselMessageParams) { p.Cards[0].Image = nil }},
		{"two media", func(p *BusinessCarouselMessageParams) { p.Cards[0].Video = carouselCard(3, true).Video }},
		{"empty controls", func(p *BusinessCarouselMessageParams) { p.Cards[0].Buttons = nil }},
		{"mixed controls", func(p *BusinessCarouselMessageParams) {
			p.Cards[0].Buttons = append(p.Cards[0].Buttons, BusinessNativeFlowButton{Name: "cta_url", ParamsJSON: `{"display_text":"Open","url":"https://example.com"}`})
		}},
		{"duplicate reply ID", func(p *BusinessCarouselMessageParams) { p.Cards[1].Buttons = p.Cards[0].Buttons }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := BusinessCarouselMessageParams{Body: "Offers", Cards: []BusinessCarouselCard{carouselCard(0, false), carouselCard(1, true)}}
			tc.mutate(&p)
			if _, err := BuildBusinessCarouselMessage(p); err == nil {
				t.Fatal("expected invalid carousel error")
			}
		})
	}
}
