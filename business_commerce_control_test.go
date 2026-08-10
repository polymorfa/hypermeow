package whatsmeow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/polymorfa/hypermeow/types"
)

func TestBuildBusinessCommerceControlVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	created, err := buildCreateBusinessCatalogVariables(jid)
	if err != nil {
		t.Fatal(err)
	}
	createInput := created["input"].(map[string]any)
	if createInput["platform"] != "WEB" || createInput["product_catalog"].(map[string]any)["biz_jid"] != jid.String() {
		t.Fatalf("unexpected catalog create input: %#v", createInput)
	}
	cart, err := buildBusinessCartVariables(jid, false)
	if err != nil || cart["input"].(map[string]any)["cart_enabled"] != false {
		t.Fatalf("cart = %#v, error = %v", cart, err)
	}
	visibility, err := buildBusinessProductVisibilityVariables(jid, "product-1", true)
	if err != nil {
		t.Fatal(err)
	}
	product := visibility["input"].(map[string]any)["products"].([]map[string]any)[0]
	if product["product_id"] != "product-1" || product["is_hidden"] != true {
		t.Fatalf("unexpected visibility input: %#v", visibility)
	}
	productAppeal, err := buildBusinessProductAppealVariables(jid, "product-1", "  incorrect rejection  ")
	if err != nil || productAppeal["input"].(map[string]any)["reason"] != "incorrect rejection" {
		t.Fatalf("product appeal = %#v, error = %v", productAppeal, err)
	}
	collectionAppeal, err := buildBusinessCollectionAppealVariables(jid, "collection-1", "incorrect rejection")
	if err != nil || collectionAppeal["input"].(map[string]any)["product_set_id"] != "collection-1" {
		t.Fatalf("collection appeal = %#v, error = %v", collectionAppeal, err)
	}
}

func TestRejectInvalidBusinessCommerceControlVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	if _, err := buildBusinessProductVisibilityVariables(jid, "", true); err == nil {
		t.Fatal("empty product ID unexpectedly passed")
	}
	for _, reason := range []string{"", "   ", strings.Repeat("r", maxBusinessCatalogAppealReasonBytes+1)} {
		if _, err := buildBusinessProductAppealVariables(jid, "product-1", reason); err == nil {
			t.Fatalf("invalid reason unexpectedly passed: %q", reason)
		}
	}
	if _, err := buildBusinessCollectionAppealVariables(jid, "", "reason"); err == nil {
		t.Fatal("empty collection ID unexpectedly passed")
	}
}

func TestDecodeBusinessCommerceControlResponses(t *testing.T) {
	for _, discriminator := range []string{
		"xfb_whatsapp_catalog_product_visibility_update",
		"xfb_whatsapp_catalog_appeal_product",
		"xfb_whatsapp_catalog_appeal_collection",
	} {
		if err := decodeBusinessCatalogSuccess(json.RawMessage(`{"`+discriminator+`":{"success":true}}`), discriminator); err != nil {
			t.Fatalf("%s success failed: %v", discriminator, err)
		}
		if err := decodeBusinessCatalogSuccess(json.RawMessage(`{"`+discriminator+`":{"success":false}}`), discriminator); err == nil {
			t.Fatalf("%s false success unexpectedly passed", discriminator)
		}
		if err := decodeBusinessCatalogSuccess(json.RawMessage(`{}`), discriminator); err == nil {
			t.Fatalf("%s missing response unexpectedly passed", discriminator)
		}
	}
	if err := decodeBusinessCatalogSuccess(json.RawMessage(`{"xfb_whatsapp_catalog_create":{"product_catalog":{"id":"catalog-1"}}}`), "xfb_whatsapp_catalog_create"); err != nil {
		t.Fatalf("catalog create response failed: %v", err)
	}
	if err := decodeBusinessCatalogSuccess(json.RawMessage(`{"xfb_whatsapp_catalog_create":{"success":true}}`), "xfb_whatsapp_catalog_create"); err == nil {
		t.Fatal("catalog create response without product_catalog unexpectedly passed")
	}
	if err := decodeBusinessCartEnabled(json.RawMessage(`{"xfb_whatsapp_smb_commerce_settings":{"cart_enabled":false}}`), false); err != nil {
		t.Fatal(err)
	}
	if err := decodeBusinessCartEnabled(json.RawMessage(`{"xfb_whatsapp_smb_commerce_settings":{"cart_enabled":true}}`), false); err == nil {
		t.Fatal("mismatched cart setting unexpectedly passed")
	}
	if err := decodeBusinessCartEnabled(json.RawMessage(`{}`), false); err == nil {
		t.Fatal("missing cart setting unexpectedly passed")
	}
}
