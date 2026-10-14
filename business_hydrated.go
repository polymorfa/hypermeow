package whatsmeow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/proto/waE2E"
)

// BusinessHydratedButtonsMessageParams is an uploaded-media hydrated template.
// Encoding this format does not imply Meta template approval or relay admission.
type BusinessHydratedButtonsMessageParams struct {
	Title, Body, Footer string
	Buttons             []BusinessNativeFlowButton
	Image               *waE2E.ImageMessage
	Video               *waE2E.VideoMessage
	Document            *waE2E.DocumentMessage
	ContextInfo         *waE2E.ContextInfo
}

// BuildBusinessHydratedButtonsMessage permits replies mixed with URL/call CTAs.
// Received Web relays additionally require a server HSM envelope.
func BuildBusinessHydratedButtonsMessage(p BusinessHydratedButtonsMessageParams) (*waE2E.Message, error) {
	if strings.TrimSpace(p.Body) == "" || !bounded(p.Body, 4096) || !bounded(p.Title, 256) || !bounded(p.Footer, 256) || len(p.Buttons) < 1 || len(p.Buttons) > 10 {
		return nil, errors.New("invalid hydrated template text or button count")
	}
	mediaCount := 0
	if p.Image != nil {
		mediaCount++
	}
	if p.Video != nil {
		mediaCount++
	}
	if p.Document != nil {
		mediaCount++
	}
	if mediaCount > 1 || (mediaCount > 0 && p.Title != "") {
		return nil, errors.New("hydrated template accepts one title or media header")
	}
	t := &waE2E.TemplateMessage_HydratedFourRowTemplate{HydratedContentText: proto.String(p.Body), HydratedFooterText: optionalString(p.Footer)}
	ids := map[string]bool{}
	ctas := 0
	for i, b := range p.Buttons {
		var params struct {
			Text  string `json:"display_text"`
			ID    string `json:"id"`
			URL   string `json:"url"`
			Phone string `json:"phone_number"`
		}
		if !bounded(b.ParamsJSON, 8192) || json.Unmarshal([]byte(b.ParamsJSON), &params) != nil || strings.TrimSpace(params.Text) == "" || !bounded(params.Text, 256) {
			return nil, fmt.Errorf("invalid hydrated button %d", i)
		}
		button := &waE2E.HydratedTemplateButton{Index: proto.Uint32(uint32(i + 1))}
		switch b.Name {
		case "quick_reply":
			if strings.TrimSpace(params.ID) == "" || !bounded(params.ID, 256) || ids[params.ID] {
				return nil, errors.New("hydrated reply IDs must be nonempty and unique")
			}
			ids[params.ID] = true
			button.HydratedButton = &waE2E.HydratedTemplateButton_QuickReplyButton{QuickReplyButton: &waE2E.HydratedTemplateButton_HydratedQuickReplyButton{DisplayText: proto.String(params.Text), ID: proto.String(params.ID)}}
		case "cta_url":
			parsed, err := url.ParseRequestURI(params.URL)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || !bounded(params.URL, 2048) {
				return nil, errors.New("hydrated URL must be absolute HTTPS")
			}
			ctas++
			button.HydratedButton = &waE2E.HydratedTemplateButton_UrlButton{UrlButton: &waE2E.HydratedTemplateButton_HydratedURLButton{DisplayText: proto.String(params.Text), URL: proto.String(params.URL)}}
		case "cta_call":
			phone := strings.TrimPrefix(params.Phone, "+")
			if len(phone) < 6 || len(phone) > 20 || strings.IndexFunc(phone, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
				return nil, errors.New("invalid hydrated call phone")
			}
			ctas++
			button.HydratedButton = &waE2E.HydratedTemplateButton_CallButton{CallButton: &waE2E.HydratedTemplateButton_HydratedCallButton{DisplayText: proto.String(params.Text), PhoneNumber: proto.String(params.Phone)}}
		default:
			return nil, errors.New("hydrated templates support replies and URL/call buttons")
		}
		t.HydratedButtons = append(t.HydratedButtons, button)
	}
	if ctas > 3 {
		return nil, errors.New("hydrated template exceeds three CTA buttons")
	}
	switch {
	case p.Image != nil:
		t.Title = &waE2E.TemplateMessage_HydratedFourRowTemplate_ImageMessage{ImageMessage: p.Image}
	case p.Video != nil:
		t.Title = &waE2E.TemplateMessage_HydratedFourRowTemplate_VideoMessage{VideoMessage: p.Video}
	case p.Document != nil:
		t.Title = &waE2E.TemplateMessage_HydratedFourRowTemplate_DocumentMessage{DocumentMessage: p.Document}
	case p.Title != "":
		t.Title = &waE2E.TemplateMessage_HydratedFourRowTemplate_HydratedTitleText{HydratedTitleText: p.Title}
	}
	return &waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{HydratedTemplate: t, ContextInfo: p.ContextInfo}}, nil
}
