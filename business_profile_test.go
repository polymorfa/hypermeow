package whatsmeow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func profileString(value string) *string {
	return &value
}

func TestBuildBusinessProfileDelta(t *testing.T) {
	websites := []string{"https://example.test", "https://shop.example.test/catalog"}
	update := types.BusinessProfileUpdate{
		Description: profileString("Synthetic tea shop"),
		Address:     profileString("1 Test Street"),
		Email:       profileString("tea@example.test"),
		Websites:    &websites,
		Hours: &types.BusinessHoursUpdate{
			TimeZone: "Asia/Beirut",
			Days: []types.BusinessHoursDay{
				{DayOfWeek: "mon", Mode: "specific_hours", OpenTime: 540, CloseTime: 1020},
				{DayOfWeek: "sun", Mode: "appointment_only"},
			},
		},
	}

	node, err := buildBusinessProfileDelta(update)
	if err != nil {
		t.Fatal(err)
	}
	if node.Tag != "business_profile" || node.AttrGetter().String("v") != "3" || node.AttrGetter().String("mutation_type") != "delta" {
		t.Fatalf("unexpected root node: %#v", node)
	}
	if got := string(node.GetChildByTag("description").Content.([]byte)); got != "Synthetic tea shop" {
		t.Fatalf("description = %q", got)
	}
	websiteNodes := node.GetChildrenByTag("website")
	if len(websiteNodes) != 2 || string(websiteNodes[1].Content.([]byte)) != websites[1] {
		t.Fatalf("unexpected websites: %#v", websiteNodes)
	}
	hours := node.GetChildByTag("business_hours")
	configs := hours.GetChildrenByTag("business_hours_config")
	if hours.AttrGetter().String("timezone") != "Asia/Beirut" || len(configs) != 2 {
		t.Fatalf("unexpected business hours: %#v", hours)
	}
	attrs := configs[0].AttrGetter()
	if attrs.String("day_of_week") != "mon" || attrs.String("mode") != "specific_hours" || attrs.String("open_time") != "540" || attrs.String("close_time") != "1020" {
		t.Fatalf("unexpected specific hours: %#v", configs[0])
	}
}

func TestBuildBusinessProfileDeltaClearsWebsites(t *testing.T) {
	websites := []string{}
	node, err := buildBusinessProfileDelta(types.BusinessProfileUpdate{Websites: &websites})
	if err != nil {
		t.Fatal(err)
	}
	websiteNodes := node.GetChildrenByTag("website")
	if len(websiteNodes) != 1 {
		t.Fatalf("website nodes = %d, want removal node", len(websiteNodes))
	}
	content, ok := websiteNodes[0].Content.([]byte)
	if !ok || len(content) != 0 {
		t.Fatalf("website removal content = %#v", websiteNodes[0].Content)
	}
}

func TestBuildBusinessProfileDeltaRejectsInvalidInput(t *testing.T) {
	tooManyWebsites := []string{"https://one.test", "https://two.test", "https://three.test"}
	tests := []types.BusinessProfileUpdate{
		{},
		{Description: profileString(strings.Repeat("d", 1025))},
		{Email: profileString("not-an-email")},
		{Websites: &tooManyWebsites},
		{Websites: &[]string{"file:///tmp/profile"}},
		{Hours: &types.BusinessHoursUpdate{TimeZone: "not/a-zone", Days: []types.BusinessHoursDay{{DayOfWeek: "mon", Mode: "open_24h"}}}},
		{Hours: &types.BusinessHoursUpdate{TimeZone: "UTC", Days: []types.BusinessHoursDay{{DayOfWeek: "mon", Mode: "specific_hours", OpenTime: -1, CloseTime: 100}}}},
		{Hours: &types.BusinessHoursUpdate{TimeZone: "UTC", Days: []types.BusinessHoursDay{{DayOfWeek: "mon", Mode: "open_24h"}, {DayOfWeek: "mon", Mode: "appointment_only"}}}},
	}
	for i, update := range tests {
		if _, err := buildBusinessProfileDelta(update); err == nil {
			t.Fatalf("case %d unexpectedly passed", i)
		}
	}
}

func TestParseBusinessProfilePreservesEditableFields(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	node := waBinary.Node{
		Tag: "business_profile",
		Content: []waBinary.Node{{
			Tag:   "profile",
			Attrs: waBinary.Attrs{"jid": jid},
			Content: []waBinary.Node{
				{Tag: "address", Content: []byte("1 Test Street")},
				{Tag: "email", Content: []byte("tea@example.test")},
				{Tag: "description", Content: []byte("Synthetic tea shop")},
				{Tag: "website", Content: []byte("https://example.test")},
				{Tag: "website", Content: []byte("https://shop.example.test")},
				{Tag: "cover_photo", Attrs: waBinary.Attrs{"id": "cover-100"}},
			},
		}},
	}

	profile, err := (&Client{}).parseBusinessProfile(&node)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Description != "Synthetic tea shop" || profile.CoverPhotoID != "cover-100" {
		t.Fatalf("unexpected profile fields: %#v", profile)
	}
	if len(profile.Websites) != 2 || profile.Websites[1] != "https://shop.example.test" {
		t.Fatalf("unexpected websites: %#v", profile.Websites)
	}
}

func TestUploadBusinessCoverPhotoUsesPlaintextPPSPath(t *testing.T) {
	image := append([]byte("\x89PNG\r\n\x1a\n"), []byte("synthetic-image")...)
	hash := sha256.Sum256(image)
	expectedToken := base64.URLEncoding.EncodeToString(hash[:])

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pps/biz-cover-photo/"+expectedToken {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
		if r.URL.Query().Get("auth") != "synthetic-auth" || r.URL.Query().Get("token") != expectedToken {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != string(image) {
			t.Fatalf("unexpected body: %q, error: %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"meta_hmac":"cover-token","fbid":"cover-100","ts":"1720000000"}`)
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		mediaHTTP: server.Client(),
		mediaConnCache: &MediaConn{
			Auth:      "synthetic-auth",
			TTL:       3600,
			FetchedAt: time.Now(),
			Hosts:     []MediaConnHost{{Hostname: serverURL.Host}},
		},
	}

	response, err := client.uploadBusinessCoverPhoto(context.Background(), image)
	if err != nil {
		t.Fatal(err)
	}
	if response.MetaHMAC != "cover-token" || response.FBID != "cover-100" || response.Timestamp != "1720000000" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBusinessCoverPhotoValidationAndNodes(t *testing.T) {
	if _, err := validateBusinessCoverPhoto([]byte("not an image")); err == nil {
		t.Fatal("expected unsupported image error")
	}
	if _, err := validateBusinessCoverPhoto(make([]byte, maxBusinessCoverPhotoBytes+1)); err == nil {
		t.Fatal("expected oversized image error")
	}
	setNode, err := buildBusinessCoverPhotoUpdateNode(businessCoverUploadResponse{MetaHMAC: "token", FBID: "cover-100", Timestamp: "1"})
	if err != nil {
		t.Fatal(err)
	}
	attrs := setNode.AttrGetter()
	if setNode.Tag != "cover_photo" || attrs.String("op") != "update" || attrs.String("id") != "cover-100" || attrs.String("token") != "token" || attrs.String("ts") != "1" {
		t.Fatalf("unexpected set node: %#v", setNode)
	}
	deleteNode, err := buildBusinessCoverPhotoDeleteNode("cover-100")
	if err != nil {
		t.Fatal(err)
	}
	if deleteNode.AttrGetter().String("op") != "delete" || deleteNode.AttrGetter().String("id") != "cover-100" {
		t.Fatalf("unexpected delete node: %#v", deleteNode)
	}
	if _, err = buildBusinessCoverPhotoDeleteNode(""); err == nil {
		t.Fatal("expected empty cover ID error")
	}
}
