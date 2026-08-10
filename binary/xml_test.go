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
			{Tag: "address", Content: []byte("12 Private Street")},
			{Tag: "email", Content: []byte("owner@example.test")},
			{Tag: "description", Content: []byte("Private description")},
			{Tag: "website", Content: []byte("https://private.example.test")},
			{Tag: "address", Content: "34 Private Street"},
			{Tag: "email", Content: "other@example.test"},
			{Tag: "description", Content: "Other private description"},
			{Tag: "website", Content: "https://other-private.example.test"},
		},
	}

	logged := node.String()
	for _, sensitive := range []string{
		"media-secret",
		"upload-secret",
		"12 Private Street",
		"owner@example.test",
		"Private description",
		"https://private.example.test",
		"34 Private Street",
		"other@example.test",
		"Other private description",
		"https://other-private.example.test",
	} {
		if strings.Contains(logged, sensitive) {
			t.Fatalf("logged sensitive value %q: %s", sensitive, logged)
		}
	}
	if strings.Count(logged, "[redacted]") != 10 {
		t.Fatalf("unexpected redacted node: %s", logged)
	}
}
