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
		Attrs: Attrs{"auth": "media-secret", "token": "upload-secret", "id": "cover-100"},
	}).String()
	if strings.Contains(logged, "media-secret") || strings.Contains(logged, "upload-secret") {
		t.Fatalf("sensitive attributes were not redacted: %s", logged)
	}
	if !strings.Contains(logged, `id="cover-100"`) || strings.Count(logged, "[redacted]") != 2 {
		t.Fatalf("unexpected redacted node: %s", logged)
	}
}
