package binary

import (
	"strings"
	"testing"
)

func TestNodeStringRedactsBusinessProfileAndCredentials(t *testing.T) {
	node := Node{
		Tag:   "business_profile",
		Attrs: Attrs{"auth": "media-secret", "token": "upload-secret"},
		Content: []Node{
			{Tag: "access_token", Content: []byte("catalog-token")},
			{Tag: "session_cookies", Content: []byte("catalog-cookie")},
			{Tag: "address", Content: []byte("12 Private Street")},
			{Tag: "email", Content: []byte("owner@example.test")},
			{Tag: "description", Content: []byte("Private description")},
			{Tag: "website", Content: []byte("https://private.example.test")},
			{Tag: "address", Content: "34 Private Street"},
			{Tag: "email", Content: "other@example.test"},
			{Tag: "description", Content: "Other private description"},
			{Tag: "website", Content: "https://other-private.example.test"},
			{Tag: "access_token", Content: "other-catalog-token"},
			{Tag: "session_cookies", Content: "other-catalog-cookie"},
		},
	}

	logged := node.String()
	for _, sensitive := range []string{
		"media-secret",
		"upload-secret",
		"catalog-token",
		"catalog-cookie",
		"12 Private Street",
		"owner@example.test",
		"Private description",
		"https://private.example.test",
		"34 Private Street",
		"other@example.test",
		"Other private description",
		"https://other-private.example.test",
		"other-catalog-token",
		"other-catalog-cookie",
	} {
		if strings.Contains(logged, sensitive) {
			t.Fatalf("logged sensitive value %q: %s", sensitive, logged)
		}
	}
	if strings.Count(logged, "[redacted]") != 14 {
		t.Fatalf("unexpected redacted node: %s", logged)
	}
}
