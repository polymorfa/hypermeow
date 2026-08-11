// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package binary

import (
	"strings"
	"testing"
)

func TestNodeStringRedactsSensitiveContent(t *testing.T) {
	for _, tag := range []string{
		"access_token",
		"address",
		"code",
		"description",
		"email",
		"session_cookies",
		"token",
		"wa_ad_account_nonce",
		"website",
	} {
		for _, contentType := range []struct {
			name  string
			value func(string) any
		}{
			{name: "bytes", value: func(secret string) any { return []byte(secret) }},
			{name: "string", value: func(secret string) any { return secret }},
		} {
			t.Run(tag+"/"+contentType.name, func(t *testing.T) {
				secret := "private-" + tag
				logged := (Node{Tag: tag, Content: contentType.value(secret)}).String()
				if strings.Contains(logged, secret) || !strings.Contains(logged, "[redacted]") {
					t.Fatalf("sensitive content was not redacted: %s", logged)
				}
			})
		}
	}
}

func TestNodeStringRedactsSensitiveAttributes(t *testing.T) {
	logged := (Node{
		Tag:   "cover_photo",
		Attrs: Attrs{"auth": "media-secret", "pin": "1234", "token": "upload-secret", "id": "cover-100"},
	}).String()
	if strings.Contains(logged, "media-secret") || strings.Contains(logged, "1234") || strings.Contains(logged, "upload-secret") {
		t.Fatalf("sensitive attributes were not redacted: %s", logged)
	}
	if !strings.Contains(logged, `id="cover-100"`) || strings.Count(logged, "[redacted]") != 3 {
		t.Fatalf("unexpected redacted node: %s", logged)
	}
}

func TestNodeStringRedactsLinkedAccountPayload(t *testing.T) {
	logged := (Node{
		Tag: "linked_accounts",
		Content: []Node{{
			Tag:   "fb_page",
			Attrs: Attrs{"id": "facebook-page-100"},
			Content: []Node{
				{Tag: "display_name", Content: []byte("Private Store")},
				{Tag: "ig_handle", Content: "private_handle"},
				{Tag: "profile_picture", Content: []Node{{Tag: "url", Content: []byte("https://private.invalid/picture?access=secret")}}},
			},
		}},
	}).String()
	for _, secret := range []string{"facebook-page-100", "Private Store", "private_handle", "private.invalid", "secret"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("linked-account payload leaked %q: %s", secret, logged)
		}
	}
	if logged != "<linked_accounts>[redacted]</linked_accounts>" {
		t.Fatalf("unexpected linked-account redaction: %s", logged)
	}
}
