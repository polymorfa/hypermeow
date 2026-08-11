// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package main

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	whatsmeow "github.com/polymorfa/hypermeow"
	"github.com/polymorfa/hypermeow/proto/waCommon"
	"github.com/polymorfa/hypermeow/proto/waE2E"
)

var (
	syntheticMediaOnce sync.Once
	syntheticMedia     map[string][]byte
)

func mediaPayload(kind string) []byte {
	syntheticMediaOnce.Do(func() {
		syntheticMedia = map[string][]byte{
			"image":    deterministicPayload(256<<10, 0x31),
			"audio":    deterministicPayload(128<<10, 0x57),
			"document": deterministicPayload(512<<10, 0x83),
			"video":    deterministicPayload(768<<10, 0xb9),
		}
	})
	return syntheticMedia[kind]
}

func deterministicPayload(size int, seed byte) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = seed + byte((i*31+i/17)%251)
	}
	return data
}

func buildWorkloadMessage(ctx context.Context, client *whatsmeow.Client, id string, sequence int64, profile string) (*waE2E.Message, string, int64, time.Duration, error) {
	correlation := "pong " + id
	if profile != "mixed" {
		return &waE2E.Message{Conversation: proto.String(correlation)}, "text", 0, 0, nil
	}

	switch sequence % 16 {
	case 0:
		return &waE2E.Message{Conversation: proto.String(correlation)}, "text-short", 0, 0, nil
	case 1:
		text := correlation + " — Build passed ✅ Visit https://polymorfa.com/docs?source=benchmark for the deployment notes."
		return &waE2E.Message{Conversation: proto.String(correlation), ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &text}}, "text-rich", 0, 0, nil
	case 2:
		text := correlation + " @12025550123 can you review this before 16:30?"
		return &waE2E.Message{Conversation: proto.String(correlation), ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: &text,
			ContextInfo: &waE2E.ContextInfo{
				MentionedJID: []string{"12025550123@s.whatsapp.net"},
				StanzaID:     proto.String("quoted-order-1847"),
				RemoteJID:    proto.String("12025550123@s.whatsapp.net"),
				QuotedMessage: &waE2E.Message{
					Conversation: proto.String("The shipment moved to dock 4."),
				},
			},
		}}, "text-quoted-mention", 0, 0, nil
	case 3:
		text := correlation + " Forwarded incident summary"
		return &waE2E.Message{Conversation: proto.String(correlation), ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: &text,
			ContextInfo: &waE2E.ContextInfo{
				IsForwarded:     proto.Bool(true),
				ForwardingScore: proto.Uint32(2),
				Expiration:      proto.Uint32(86400),
			},
		}}, "text-forwarded-ephemeral", 0, 0, nil
	case 4:
		return &waE2E.Message{Conversation: proto.String(correlation), LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  proto.Float64(47.3769),
			DegreesLongitude: proto.Float64(8.5417),
			Name:             proto.String("Zürich HB"),
			Address:          proto.String("Bahnhofplatz, 8001 Zürich"),
			Comment:          proto.String("Meet by the main hall at 18:10"),
		}}, "location", 0, 0, nil
	case 5:
		return &waE2E.Message{Conversation: proto.String(correlation), ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String("Maya Chen"),
			Vcard:       proto.String("BEGIN:VCARD\nVERSION:3.0\nFN:Maya Chen\nTEL;TYPE=CELL:+12025550142\nEMAIL:maya@example.test\nEND:VCARD"),
		}}, "contact", 0, 0, nil
	case 6:
		return &waE2E.Message{Conversation: proto.String(correlation), PollCreationMessage: &waE2E.PollCreationMessage{
			EncKey:                 deterministicPayload(32, 0x22),
			Name:                   proto.String("Which release window works best?"),
			Options:                []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("Tuesday 09:00")}, {OptionName: proto.String("Wednesday 14:00")}, {OptionName: proto.String("Thursday 17:30")}},
			SelectableOptionsCount: proto.Uint32(1),
		}}, "poll", 0, 0, nil
	case 7:
		return &waE2E.Message{Conversation: proto.String(correlation), ReactionMessage: &waE2E.ReactionMessage{
			Key: &waCommon.MessageKey{
				RemoteJID: proto.String("12025550123@s.whatsapp.net"),
				ID:        proto.String("3EB0A1B2C3D4E5F60789"),
			},
			Text:              proto.String("👍🏽"),
			SenderTimestampMS: proto.Int64(1760000000000 + sequence),
		}}, "reaction", 0, 0, nil
	case 8:
		return buildMediaMessage(ctx, client, correlation, "image", false)
	case 9:
		return buildMediaMessage(ctx, client, correlation, "audio", false)
	case 10:
		return buildMediaMessage(ctx, client, correlation, "document", false)
	case 11:
		return buildMediaMessage(ctx, client, correlation, "video", false)
	case 12:
		return buildMediaMessage(ctx, client, correlation, "image", true)
	case 13:
		text := correlation + " Order #A-1847 is ready for pickup"
		return &waE2E.Message{Conversation: proto.String(correlation), ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:        &text,
			MatchedText: proto.String("https://shop.example.test/orders/A-1847"),
			Title:       proto.String("Pickup ready"),
			Description: proto.String("Dock 4 · closes at 19:00"),
		}}, "link-preview", 0, 0, nil
	case 14:
		text := correlation + " This update disappears after it is read."
		return &waE2E.Message{Conversation: proto.String(correlation), ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: &text,
			ContextInfo: &waE2E.ContextInfo{
				Expiration:        proto.Uint32(3600),
				AfterReadDuration: proto.Uint32(30),
				IsSpoiler:         proto.Bool(true),
			},
		}}, "ephemeral-spoiler", 0, 0, nil
	default:
		text := correlation + " The customer confirmed delivery; no follow-up is needed."
		return &waE2E.Message{Conversation: proto.String(correlation), ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &text}}, "text-medium", 0, 0, nil
	}
}

func buildMediaMessage(ctx context.Context, client *whatsmeow.Client, correlation, kind string, viewOnce bool) (*waE2E.Message, string, int64, time.Duration, error) {
	payload := mediaPayload(kind)
	var mediaType whatsmeow.MediaType
	switch kind {
	case "image":
		mediaType = whatsmeow.MediaImage
	case "audio":
		mediaType = whatsmeow.MediaAudio
	case "document":
		mediaType = whatsmeow.MediaDocument
	case "video":
		mediaType = whatsmeow.MediaVideo
	}
	started := time.Now()
	var upload whatsmeow.UploadResponse
	var err error
	if kind == "document" || kind == "video" {
		upload, err = client.UploadReader(ctx, bytes.NewReader(payload), nil, mediaType)
	} else {
		upload, err = client.Upload(ctx, payload, mediaType)
	}
	duration := time.Since(started)
	if err != nil {
		return nil, kind, int64(len(payload)), duration, fmt.Errorf("upload %s fixture: %w", kind, err)
	}
	message := &waE2E.Message{Conversation: proto.String(correlation)}
	switch kind {
	case "image":
		message.ImageMessage = &waE2E.ImageMessage{URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256, FileLength: &upload.FileLength, Mimetype: proto.String("image/jpeg"), Caption: proto.String("Warehouse shelf after the morning restock"), Width: proto.Uint32(1280), Height: proto.Uint32(960), ViewOnce: &viewOnce}
	case "audio":
		message.AudioMessage = &waE2E.AudioMessage{URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256, FileLength: &upload.FileLength, Mimetype: proto.String("audio/ogg; codecs=opus"), Seconds: proto.Uint32(12), PTT: proto.Bool(true), Waveform: deterministicPayload(64, 0x19)}
	case "document":
		message.DocumentMessage = &waE2E.DocumentMessage{URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256, FileLength: &upload.FileLength, Mimetype: proto.String("application/pdf"), Title: proto.String("August dispatch manifest"), FileName: proto.String("dispatch-manifest-2026-08.pdf"), PageCount: proto.Uint32(7)}
	case "video":
		message.VideoMessage = &waE2E.VideoMessage{URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256, FileLength: &upload.FileLength, Mimetype: proto.String("video/mp4"), Caption: proto.String("Loading-bay walkthrough"), Seconds: proto.Uint32(9), Width: proto.Uint32(1280), Height: proto.Uint32(720)}
	}
	category := kind
	if viewOnce {
		category += "-view-once"
	}
	return message, category, int64(len(payload)), duration, nil
}
