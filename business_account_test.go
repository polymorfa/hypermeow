package whatsmeow

import (
	"strings"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

func TestBusinessLinkedAccountsQuery(t *testing.T) {
	query := businessLinkedAccountsQuery()
	if query.Namespace != "fb:thrift_iq" || query.Type != iqGet || query.To != types.ServerJID || query.SMaxID != "42" {
		t.Fatalf("unexpected linked accounts query: %#v", query)
	}
	content, ok := query.Content.([]waBinary.Node)
	if !ok || len(content) != 1 || content[0].Tag != "linked_accounts" {
		t.Fatalf("unexpected linked accounts content: %#v", query.Content)
	}
}

func TestParseBusinessLinkedAccounts(t *testing.T) {
	response := waBinary.Node{Tag: "iq", Content: []waBinary.Node{{
		Tag: "linked_accounts",
		Content: []waBinary.Node{
			{Tag: "fb_page", Attrs: waBinary.Attrs{"id": "page-1"}, Content: []waBinary.Node{
				{Tag: "profile_sync", Attrs: waBinary.Attrs{"state": "import"}},
				{Tag: "display_name", Content: []byte("Synthetic Page")},
				{Tag: "ad_status", Attrs: waBinary.Attrs{"has_active_ctwa_ad": "true", "has_created_ad": "false"}},
				{Tag: "profile_picture", Content: []waBinary.Node{{Tag: "bytes", Content: []byte("ignored")}, {Tag: "url", Content: []byte("https://example.test/page.jpg")}}},
				{Tag: "show_on_profile", Content: []byte("true")},
				{Tag: "whatsapp_as_page_button", Attrs: waBinary.Attrs{"state": "on"}},
			}},
			{Tag: "fb_biz", Attrs: waBinary.Attrs{"id": "business-1"}, Content: []waBinary.Node{
				{Tag: "catalog", Attrs: waBinary.Attrs{"id": "catalog-1", "state": "import"}},
				{Tag: "display_name", Content: []byte("Synthetic Business")},
			}},
			{Tag: "ig_professional", Content: []waBinary.Node{
				{Tag: "ig_handle", Content: []byte("synthetic_shop")},
				{Tag: "profile_picture", Content: []waBinary.Node{{Tag: "url", Content: []byte("https://example.test/ig.jpg")}}},
				{Tag: "display_name", Content: []byte("Synthetic Shop")},
				{Tag: "show_on_profile", Content: []byte("false")},
			}},
			{Tag: "whatsapp_ad_identity", Attrs: waBinary.Attrs{"id": "identity-1"}, Content: []waBinary.Node{
				{Tag: "ad_status", Attrs: waBinary.Attrs{"has_active_ctwa_ad": "false", "has_created_ad": "true"}},
			}},
		},
	}}}

	accounts, err := parseBusinessLinkedAccounts(&response)
	if err != nil {
		t.Fatal(err)
	}
	if accounts.FacebookPage == nil || accounts.FacebookPage.ID != "page-1" || !accounts.FacebookPage.ShowOnProfile || accounts.FacebookPage.ProfilePictureURL != "https://example.test/page.jpg" {
		t.Fatalf("unexpected Facebook Page: %#v", accounts.FacebookPage)
	}
	if accounts.FacebookBusiness == nil || accounts.FacebookBusiness.CatalogID != "catalog-1" || accounts.FacebookBusiness.CatalogState != "import" {
		t.Fatalf("unexpected Facebook business: %#v", accounts.FacebookBusiness)
	}
	if accounts.InstagramProfessional == nil || accounts.InstagramProfessional.Handle != "synthetic_shop" || accounts.InstagramProfessional.ShowOnProfile {
		t.Fatalf("unexpected Instagram account: %#v", accounts.InstagramProfessional)
	}
	if accounts.WhatsAppAdIdentity == nil || accounts.WhatsAppAdIdentity.HasActiveCTWAAd || !accounts.WhatsAppAdIdentity.HasCreatedAd {
		t.Fatalf("unexpected WhatsApp ad identity: %#v", accounts.WhatsAppAdIdentity)
	}
}

func TestParseBusinessLinkedAccountsRejectsMalformedValues(t *testing.T) {
	response := waBinary.Node{Tag: "iq", Content: []waBinary.Node{{Tag: "linked_accounts", Content: []waBinary.Node{{
		Tag: "fb_page", Attrs: waBinary.Attrs{"id": "page-1"}, Content: []waBinary.Node{
			{Tag: "display_name", Content: []byte("Synthetic Page")},
			{Tag: "ad_status", Attrs: waBinary.Attrs{"has_active_ctwa_ad": "maybe", "has_created_ad": "false"}},
			{Tag: "profile_picture", Content: []waBinary.Node{{Tag: "url", Content: []byte("https://example.test/page.jpg")}}},
			{Tag: "show_on_profile", Content: []byte("true")},
			{Tag: "whatsapp_as_page_button", Attrs: waBinary.Attrs{"state": "on"}},
		},
	}}}}}
	if _, err := parseBusinessLinkedAccounts(&response); err == nil {
		t.Fatal("expected malformed boolean error")
	}
}

func TestBusinessEligibilityQuery(t *testing.T) {
	query, err := businessEligibilityQuery(nil)
	if err != nil {
		t.Fatal(err)
	}
	if query.Namespace != "w:biz" || query.Type != iqGet || query.To != types.ServerJID || query.SMaxID != "139" {
		t.Fatalf("unexpected eligibility query: %#v", query)
	}
	content := query.Content.([]waBinary.Node)
	attrs := content[0].Attrs
	for _, feature := range businessEligibilityFeatures {
		if attrs[string(feature)] != "true" {
			t.Fatalf("feature %q was not requested: %#v", feature, attrs)
		}
	}
	if _, err = businessEligibilityQuery([]types.BusinessFeature{types.BusinessFeatureGenAI, types.BusinessFeatureGenAI}); err == nil {
		t.Fatal("expected duplicate feature error")
	}
	if _, err = businessEligibilityQuery([]types.BusinessFeature{"unknown"}); err == nil {
		t.Fatal("expected unknown feature error")
	}
}

func TestParseBusinessEligibility(t *testing.T) {
	response := waBinary.Node{Tag: "iq", Content: []waBinary.Node{
		{Tag: "meta_verified", Attrs: waBinary.Attrs{"status": "SUCCESS", "additional_params": "{}", "should_show_privacy_interstitial_to_new_users": "false"}},
		{Tag: "marketing_messages", Attrs: waBinary.Attrs{"status": "PAUSED", "expiration": "1720000000"}},
		{Tag: "genai", Attrs: waBinary.Attrs{"status": "SUCCESS", "v1_enabled": "true"}},
		{Tag: "bb_pro", Attrs: waBinary.Attrs{"status": "ELIGIBLE_TO_ONBOARD"}},
	}}
	eligibility, err := parseBusinessEligibility(&response)
	if err != nil {
		t.Fatal(err)
	}
	if len(eligibility.Features) != 4 || eligibility.Features[1].Expiration != 1720000000 {
		t.Fatalf("unexpected eligibility: %#v", eligibility)
	}
	if eligibility.Features[0].ShowPrivacyInterstitial == nil || *eligibility.Features[0].ShowPrivacyInterstitial {
		t.Fatalf("unexpected privacy interstitial value: %#v", eligibility.Features[0])
	}
	if eligibility.Features[2].V1Enabled == nil || !*eligibility.Features[2].V1Enabled {
		t.Fatalf("unexpected genai value: %#v", eligibility.Features[2])
	}
}

func TestParseBusinessEligibilityRejectsOversizedAdditionalParams(t *testing.T) {
	response := waBinary.Node{Tag: "iq", Content: []waBinary.Node{{
		Tag: "meta_verified", Attrs: waBinary.Attrs{"status": "SUCCESS", "additional_params": strings.Repeat("x", maxBusinessEligibilityParamsBytes+1)},
	}}}
	if _, err := parseBusinessEligibility(&response); err == nil {
		t.Fatal("expected oversized additional params error")
	}
}
