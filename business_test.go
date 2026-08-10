package whatsmeow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
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

func TestBuildCatalogVariablesRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		jid  types.JID
		p    GetCatalogParams
	}{
		{"empty jid", types.EmptyJID, GetCatalogParams{}},
		{"server jid", types.ServerJID, GetCatalogParams{}},
		{"empty user jid", types.NewJID("", types.DefaultUserServer), GetCatalogParams{}},
		{"group jid", types.NewJID("123", types.GroupServer), GetCatalogParams{}},
		{"limit too large", types.NewJID("123", types.DefaultUserServer), GetCatalogParams{Limit: 101}},
		{"negative width", types.NewJID("123", types.DefaultUserServer), GetCatalogParams{Width: -1}},
		{"height too large", types.NewJID("123", types.DefaultUserServer), GetCatalogParams{Height: 1025}},
		{"cursor too large", types.NewJID("123", types.DefaultUserServer), GetCatalogParams{After: string(make([]byte, 2049))}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildCatalogVariables(tc.jid, tc.p); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDecodeCatalogPagePreservesCommerceFields(t *testing.T) {
	raw := json.RawMessage(`{"xwa_product_catalog_get_product_catalog":{"product_catalog":{"paging":{"after":"next"},"products":[{"id":"p1","retailer_id":"sku-1","name":"Tea","description":"Green tea","price":"1250","currency":"USD","is_hidden":false,"is_sanctioned":false,"max_available":8,"product_availability":"in stock","media":{"images":[{"id":"i1","request_image_url":"https://synthetic.invalid/i1"}]},"status_info":{"can_appeal":true,"status":"APPROVED"}}]}}}`)
	page, err := decodeCatalogPage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if page.Next != "next" || len(page.Products) != 1 {
		t.Fatalf("unexpected page: %#v", page)
	}
	product := page.Products[0]
	if product.ID != "p1" || product.RetailerID != "sku-1" || product.Price != "1250" || product.Currency != "USD" || product.MaxAvailable != 8 {
		t.Fatalf("unexpected product: %#v", product)
	}
	if len(product.Media.Images) != 1 || product.Media.Images[0].RequestURL != "https://synthetic.invalid/i1" || !product.Status.CanAppeal {
		t.Fatalf("unexpected nested product fields: %#v", product)
	}
}

func TestDecodeCatalogPageFailsClosedWithoutDiscriminator(t *testing.T) {
	if _, err := decodeCatalogPage(json.RawMessage(`{"unexpected":{}}`)); err == nil {
		t.Fatal("expected response discriminator error")
	}
}

func TestBuildCatalogProductVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	variables, err := buildCatalogProductVariables(jid, "p-tea", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	product := variables["request"].(map[string]any)["product"].(map[string]any)
	if product["jid"] != jid.String() || product["product_id"] != "p-tea" || product["width"] != "100" || product["fetch_compliance_info"] != "true" {
		t.Fatalf("unexpected variables: %#v", variables)
	}
	if _, err = buildCatalogProductVariables(jid, "", 100, 100); err == nil {
		t.Fatal("expected empty product ID error")
	}
}

func TestDecodeCatalogProductRequiresProduct(t *testing.T) {
	raw := json.RawMessage(`{"xwa_product_catalog_get_product":{"product_catalog":{"product":{"id":"p-tea","name":"Tea","price":"1250","currency":"USD"}}}}`)
	product, err := decodeCatalogProduct(raw)
	if err != nil {
		t.Fatal(err)
	}
	if product.ID != "p-tea" || product.Price != "1250" {
		t.Fatalf("unexpected product: %#v", product)
	}
	if _, err = decodeCatalogProduct(json.RawMessage(`{"xwa_product_catalog_get_product":{"product_catalog":{}}}`)); err == nil {
		t.Fatal("expected missing product error")
	}
}

func TestBuildCollectionsVariablesAppliesIndependentBounds(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	variables, err := buildCollectionsVariables(jid, GetCollectionsParams{})
	if err != nil {
		t.Fatal(err)
	}
	collections := variables["request"].(map[string]any)["collections"].(map[string]any)
	if collections["biz_jid"] != jid.String() || collections["collection_limit"] != "20" || collections["item_limit"] != "50" {
		t.Fatalf("unexpected variables: %#v", variables)
	}
	if _, err = buildCollectionsVariables(jid, GetCollectionsParams{CollectionLimit: 21}); err == nil {
		t.Fatal("expected collection limit error")
	}
	if _, err = buildCollectionsVariables(jid, GetCollectionsParams{ItemLimit: 101}); err == nil {
		t.Fatal("expected item limit error")
	}
}

func TestDecodeCollectionsPreservesCursorAndProducts(t *testing.T) {
	raw := json.RawMessage(`{"xwa_product_catalog_get_collections":{"collections":[{"id":"c-summer","name":"Summer","products":[{"id":"p-tea","name":"Tea","price":"1250","currency":"USD"}],"status_info":{"status":"APPROVED","can_appeal":false}}],"paging":{"after":"next"}}}`)
	page, err := decodeCollections(raw)
	if err != nil {
		t.Fatal(err)
	}
	if page.Next != "next" || len(page.Collections) != 1 || page.Collections[0].Products[0].ID != "p-tea" || page.Collections[0].Status.Status != "APPROVED" {
		t.Fatalf("unexpected collections: %#v", page)
	}
}

func TestBuildSingleCollectionAndDecode(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	variables, err := buildSingleCollectionVariables(jid, "c-summer", GetCatalogParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	collectionRequest := variables["request"].(map[string]any)["collection"].(map[string]any)
	if collectionRequest["biz_jid"] != jid.String() || collectionRequest["id"] != "c-summer" || collectionRequest["limit"] != "10" {
		t.Fatalf("unexpected variables: %#v", variables)
	}
	raw := json.RawMessage(`{"xwa_product_catalog_get_single_collection":{"collection":{"id":"c-summer","name":"Summer","products":[]},"paging":{"after":"next","before":"previous"}}}`)
	collection, err := decodeSingleCollection(raw)
	if err != nil || collection.ID != "c-summer" || collection.Next != "next" || collection.Previous != "previous" {
		t.Fatalf("collection = %#v, error = %v", collection, err)
	}
}

func TestProductListRejectsDuplicatesAndPreservesRequestedOrder(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	if _, err := buildProductListVariables(jid, []string{"p-tea", "p-tea"}, 100, 100); err == nil {
		t.Fatal("expected duplicate product ID error")
	}
	raw := json.RawMessage(`{"xwa_product_catalog_get_product_list":{"product_list":{"products":[{"id":"p-coffee","name":"Coffee","price":"1400","currency":"USD"},{"id":"p-tea","name":"Tea","price":"1250","currency":"USD"}]}}}`)
	products, err := decodeProductList(raw, []string{"p-tea", "p-coffee"})
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 2 || products[0].ID != "p-tea" || products[1].ID != "p-coffee" {
		t.Fatalf("unexpected product order: %#v", products)
	}
}

func TestParseOrderDetailsRejectsMalformedMoney(t *testing.T) {
	node := waBinary.Node{
		Tag:   "order",
		Attrs: waBinary.Attrs{"id": "o-100", "creation_ts": "1"},
		Content: []waBinary.Node{{
			Tag: "price",
			Content: []waBinary.Node{
				{Tag: "subtotal", Content: []byte("1250")},
				{Tag: "total", Content: []byte("not-a-number")},
				{Tag: "currency", Content: []byte("USD")},
			},
		}},
	}
	if _, err := parseOrderDetailsNode(node); err == nil {
		t.Fatal("expected malformed total error")
	}
}

func TestValidateOrderLookupBounds(t *testing.T) {
	tests := []struct {
		orderID string
		token   string
	}{
		{"", "token"},
		{"o-100", ""},
		{strings.Repeat("o", 257), "token"},
		{"o-100", strings.Repeat("x", 8193)},
	}
	for _, tc := range tests {
		if err := validateOrderLookup(tc.orderID, tc.token); err == nil {
			t.Fatalf("validateOrderLookup(%d-byte ID, %d-byte token) unexpectedly passed", len(tc.orderID), len(tc.token))
		}
	}
}

func TestValidateOrderResponseIDRejectsDifferentOrder(t *testing.T) {
	if err := validateOrderResponseID("o-100", "o-101"); err == nil {
		t.Fatal("expected mismatched order ID error")
	}
}

func TestBuildCatalogVariablesAppliesDefaults(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	variables, err := buildCatalogVariables(jid, GetCatalogParams{})
	if err != nil {
		t.Fatal(err)
	}
	productCatalog := variables["request"].(map[string]any)["product_catalog"].(map[string]any)
	if productCatalog["jid"] != jid.String() || productCatalog["limit"] != "50" || productCatalog["width"] != "100" || productCatalog["height"] != "100" {
		t.Fatalf("unexpected variables: %#v", variables)
	}
}

func TestBuildCreateBusinessCollectionVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	variables, err := buildCreateBusinessCollectionVariables(jid, "  Summer tea  ", []string{"product-1", "product-2"}, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	collection := variables["input"].(map[string]any)["collection"].(map[string]any)
	if collection["name"] != "Summer tea" || collection["biz_jid"] != jid.String() || collection["catalog_session_id"] != "session-1" {
		t.Fatalf("unexpected collection: %#v", collection)
	}
	if len(collection["product_ids"].([]string)) != 2 {
		t.Fatalf("unexpected product IDs: %#v", collection)
	}
	for _, test := range []struct {
		name       string
		productIDs []string
	}{
		{"", []string{"product-1"}},
		{strings.Repeat("n", 257), []string{"product-1"}},
		{"Tea", nil},
		{"Tea", []string{"same", "same"}},
	} {
		if _, err = buildCreateBusinessCollectionVariables(jid, test.name, test.productIDs, "session-1"); err == nil {
			t.Fatalf("invalid create unexpectedly passed: %#v", test)
		}
	}
}

func TestBuildUpdateBusinessCollectionVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	name := "Tea gifts"
	variables, err := buildUpdateBusinessCollectionVariables(jid, "collection-1", types.BusinessCollectionUpdate{
		Name: &name, AddProductIDs: []string{"product-3"}, RemoveProductIDs: []string{"product-1"},
	}, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	collection := variables["input"].(map[string]any)["collection"].(map[string]any)
	if collection["id"] != "collection-1" || collection["name"] != "Tea gifts" {
		t.Fatalf("unexpected update: %#v", collection)
	}
	if collection["add"].(map[string]any)["ids"].([]string)[0] != "product-3" || collection["remove"].(map[string]any)["ids"].([]string)[0] != "product-1" {
		t.Fatalf("unexpected membership update: %#v", collection)
	}
	for _, update := range []types.BusinessCollectionUpdate{
		{},
		{AddProductIDs: []string{"same"}, RemoveProductIDs: []string{"same"}},
		{AddProductIDs: []string{"same", "same"}},
	} {
		if _, err = buildUpdateBusinessCollectionVariables(jid, "collection-1", update, "session-1"); err == nil {
			t.Fatalf("invalid update unexpectedly passed: %#v", update)
		}
	}
}

func TestBuildDeleteAndReorderBusinessCollectionsVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	deleted, err := buildDeleteBusinessCollectionsVariables(jid, []string{"collection-1", "collection-2"}, "session-1")
	if err != nil || deleted["input"].(map[string]any)["collections"] == nil {
		t.Fatalf("delete = %#v, error = %v", deleted, err)
	}
	moves := []types.BusinessCollectionMove{{CollectionID: "collection-2", FromIndex: 1, ToIndex: 0}}
	reordered, err := buildReorderBusinessCollectionsVariables(jid, moves)
	if err != nil {
		t.Fatal(err)
	}
	move := reordered["input"].(map[string]any)["move"].([]map[string]any)[0]
	if move["collection_id"] != "collection-2" || move["from_index"] != 1 || move["to_index"] != 0 {
		t.Fatalf("unexpected move: %#v", move)
	}
	if _, err = buildDeleteBusinessCollectionsVariables(jid, []string{"same", "same"}, "session-1"); err == nil {
		t.Fatal("duplicate delete unexpectedly passed")
	}
	if _, err = buildReorderBusinessCollectionsVariables(jid, []types.BusinessCollectionMove{{CollectionID: "collection-1", FromIndex: -1, ToIndex: 0}}); err == nil {
		t.Fatal("negative move unexpectedly passed")
	}
}

func TestDecodeBusinessCollectionMutationResponses(t *testing.T) {
	created, err := decodeBusinessCollectionMutation(json.RawMessage(`{"xfb_whatsapp_catalog_create_collection":{"collection":{"id":"collection-1","status_info":{"status":"PENDING"}}}}`), "xfb_whatsapp_catalog_create_collection")
	if err != nil || created.ID != "collection-1" || created.ReviewStatus != "PENDING" {
		t.Fatalf("created = %#v, error = %v", created, err)
	}
	updated, err := decodeBusinessCollectionMutation(json.RawMessage(`{"xfb_whatsapp_catalog_update_collection":{"collection":{"id":"collection-1","status_info":{"status":"APPROVED"}}}}`), "xfb_whatsapp_catalog_update_collection")
	if err != nil || updated.ReviewStatus != "APPROVED" {
		t.Fatalf("updated = %#v, error = %v", updated, err)
	}
	for _, discriminator := range []string{"xfb_whatsapp_catalog_delete_collections", "xfb_whatsapp_catalog_update_collection_list"} {
		if err = decodeBusinessCatalogSuccess(json.RawMessage(`{"`+discriminator+`":{"success":true}}`), discriminator); err != nil {
			t.Fatalf("%s success failed: %v", discriminator, err)
		}
		if err = decodeBusinessCatalogSuccess(json.RawMessage(`{"`+discriminator+`":{"success":false}}`), discriminator); err == nil {
			t.Fatalf("%s false success unexpectedly passed", discriminator)
		}
	}
}

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

type merchantComplianceRoundTripper func(*http.Request) (*http.Response, error)

func (fn merchantComplianceRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func syntheticMerchantCompliance() types.BusinessMerchantCompliance {
	return types.BusinessMerchantCompliance{
		EntityName:       "Polymorfa Labs",
		EntityType:       types.BusinessMerchantEntityPrivateCompany,
		IsRegistered:     true,
		EntityTypeCustom: "",
		CustomerCare: types.BusinessMerchantContact{
			Email:          "support@example.test",
			LandlineNumber: "+961 1 555 0100",
			MobileNumber:   "+961 70 555 010",
		},
		GrievanceOfficer: types.BusinessMerchantOfficer{
			Name:           "Compliance Desk",
			Email:          "appeals@example.test",
			LandlineNumber: "+961 1 555 0101",
			MobileNumber:   "+961 70 555 011",
		},
	}
}

func TestBuildBusinessMerchantComplianceVariables(t *testing.T) {
	got, err := buildBusinessMerchantComplianceVariables(types.NewJID("15550001111", types.DefaultUserServer), syntheticMerchantCompliance())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"input": map[string]any{
		"biz_jid": "15550001111@s.whatsapp.net",
		"merchant_info": map[string]any{
			"entity_name":        "Polymorfa Labs",
			"entity_type":        "PRIVATE_COMPANY",
			"is_registered":      true,
			"entity_type_custom": "",
			"customer_care_details": map[string]any{
				"email": "support@example.test", "landline_number": "+961 1 555 0100", "mobile_number": "+961 70 555 010",
			},
			"grievance_officer_details": map[string]any{
				"name": "Compliance Desk", "email": "appeals@example.test", "landline_number": "+961 1 555 0101", "mobile_number": "+961 70 555 011",
			},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected variables:\n got %#v\nwant %#v", got, want)
	}
}

func TestBuildBusinessMerchantComplianceQueryVariables(t *testing.T) {
	got, err := buildBusinessMerchantComplianceQueryVariables(types.NewJID("15550001111", types.DefaultUserServer))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"request": map[string]any{"biz_jid": "15550001111@s.whatsapp.net"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected variables: got %#v want %#v", got, want)
	}
}

func TestDecodeBusinessMerchantCompliance(t *testing.T) {
	data := json.RawMessage(`{"xfb_whatsapp_biz_merchant_compliance_info":{"merchant_info":{"entity_name":"Polymorfa Labs","entity_type":"PRIVATE_COMPANY","is_registered":true,"entity_type_custom":"","customer_care_details":{"email":"support@example.test","landline_number":"+961 1 555 0100","mobile_number":"+961 70 555 010"},"grievance_officer_details":{"name":"Compliance Desk","email":"appeals@example.test","landline_number":"+961 1 555 0101","mobile_number":"+961 70 555 011"}}}}`)
	got, err := decodeBusinessMerchantCompliance(data, "xfb_whatsapp_biz_merchant_compliance_info")
	if err != nil {
		t.Fatal(err)
	}
	want := syntheticMerchantCompliance()
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("unexpected compliance response: got %#v want %#v", *got, want)
	}
}

func TestBusinessMerchantComplianceRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.BusinessMerchantCompliance)
	}{
		{name: "entity type", mutate: func(info *types.BusinessMerchantCompliance) { info.EntityType = "COOPERATIVE" }},
		{name: "missing entity type", mutate: func(info *types.BusinessMerchantCompliance) { info.EntityType = "" }},
		{name: "missing custom entity type", mutate: func(info *types.BusinessMerchantCompliance) {
			info.EntityType = types.BusinessMerchantEntityOther
			info.EntityTypeCustom = "   "
		}},
		{name: "empty entity name", mutate: func(info *types.BusinessMerchantCompliance) { info.EntityName = "   " }},
		{name: "entity name length", mutate: func(info *types.BusinessMerchantCompliance) { info.EntityName = strings.Repeat("n", 257) }},
		{name: "customer email length", mutate: func(info *types.BusinessMerchantCompliance) { info.CustomerCare.Email = strings.Repeat("e", 255) }},
		{name: "officer phone length", mutate: func(info *types.BusinessMerchantCompliance) {
			info.GrievanceOfficer.MobileNumber = strings.Repeat("1", 65)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := syntheticMerchantCompliance()
			tc.mutate(&info)
			if _, err := buildBusinessMerchantComplianceVariables(types.NewJID("15550001111", types.DefaultUserServer), info); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDecodeBusinessMerchantComplianceRejectsMissingPayload(t *testing.T) {
	if _, err := decodeBusinessMerchantCompliance(json.RawMessage(`{"xfb_whatsapp_biz_merchant_compliance_info":{}}`), "xfb_whatsapp_biz_merchant_compliance_info"); err == nil {
		t.Fatal("expected missing merchant_info error")
	}
}

func TestBusinessMerchantComplianceMethodsUseMatchingGraphEnvironments(t *testing.T) {
	jid := types.NewJID("15550001111", types.DefaultUserServer)
	client := NewClient(&store.Device{ID: &jid}, waLog.Noop)
	client.getBusinessCatalogAuth().token = businessAccessToken{accessToken: "synthetic-ad-token", actorID: "synthetic-actor"}
	client.mediaHTTP = &http.Client{Transport: merchantComplianceRoundTripper(func(request *http.Request) (*http.Response, error) {
		var body struct {
			AccessToken string         `json:"access_token"`
			DocumentID  string         `json:"doc_id"`
			Variables   map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		var payload string
		switch body.DocumentID {
		case businessGetMerchantComplianceDocumentID:
			if request.URL.String() != businessCatalogGraphQLEndpoint || body.AccessToken != businessCatalogGraphQLAccessToken || body.Variables["request"] == nil {
				return nil, fmt.Errorf("unexpected catalog query: %s %#v", request.URL, body)
			}
			payload = `{"data":{"xfb_whatsapp_biz_merchant_compliance_info":{"merchant_info":{"entity_name":"Polymorfa Labs","entity_type":"PRIVATE_COMPANY","is_registered":true,"entity_type_custom":"","customer_care_details":{},"grievance_officer_details":{}}}}}`
		case businessSetMerchantComplianceDocumentID:
			input, _ := body.Variables["input"].(map[string]any)
			if request.URL.String() != businessGraphQLEndpoint || body.AccessToken != "synthetic-ad-token" || input["actor_id"] != "synthetic-actor" {
				return nil, fmt.Errorf("unexpected Facebook mutation: %s %#v", request.URL, body)
			}
			payload = `{"data":{"xfb_whatsapp_biz_merchant_set_compliance_info":{"merchant_info":{"entity_name":"Polymorfa Labs","entity_type":"PRIVATE_COMPANY","is_registered":true,"entity_type_custom":"","customer_care_details":{},"grievance_officer_details":{}}}}}`
		default:
			return nil, fmt.Errorf("unexpected document ID %q", body.DocumentID)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(payload))}, nil
	})}

	read, err := client.GetBusinessMerchantCompliance(context.Background())
	if err != nil || read.EntityName != "Polymorfa Labs" {
		t.Fatalf("read = %#v, error = %v", read, err)
	}
	updated, err := client.SetBusinessMerchantCompliance(context.Background(), syntheticMerchantCompliance())
	if err != nil || updated.EntityType != types.BusinessMerchantEntityPrivateCompany {
		t.Fatalf("updated = %#v, error = %v", updated, err)
	}
}

func TestBuildBusinessProductMessageMatchesWebGenerator(t *testing.T) {
	msg, err := BuildBusinessProductMessage(BusinessProductMessageParams{
		BusinessOwnerJID: types.NewJID("15550001", types.DefaultUserServer),
		ProductID:        "p-tea", Title: "Green tea", Description: "Twenty sachets",
		CurrencyCode: "USD", PriceAmount1000: 1250, SalePriceAmount1000: 1100,
		RetailerID: "sku-tea", URL: "https://synthetic.invalid/products/p-tea",
		ProductImageCount: 1, ProductImage: &waE2E.ImageMessage{URL: testPtr("https://synthetic.invalid/media/tea")},
		Body: "Our most popular tea", Footer: "Seasonal catalog",
		ContextInfo: &waE2E.ContextInfo{MentionedJID: []string{"15550002@s.whatsapp.net"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	product := msg.GetProductMessage()
	if product.GetBusinessOwnerJID() != "15550001@s.whatsapp.net" || product.GetBody() != "Our most popular tea" || product.GetFooter() != "Seasonal catalog" || len(product.GetContextInfo().GetMentionedJID()) != 1 {
		t.Fatalf("unexpected envelope: %#v", product)
	}
	snapshot := product.GetProduct()
	if snapshot.GetProductID() != "p-tea" || snapshot.GetPriceAmount1000() != 1250 || snapshot.GetSalePriceAmount1000() != 1100 || snapshot.GetProductImage().GetURL() == "" {
		t.Fatalf("unexpected product snapshot: %#v", snapshot)
	}
}

func TestBuildBusinessProductMessagePreservesExplicitZeroPrice(t *testing.T) {
	msg, err := BuildBusinessProductMessage(BusinessProductMessageParams{
		BusinessOwnerJID: types.NewJID("15550001", types.DefaultUserServer),
		ProductID:        "p-free",
		Title:            "Free sample",
		CurrencyCode:     "USD",
		PriceAmount1000:  0,
	})
	if err != nil {
		t.Fatal(err)
	}
	price := msg.GetProductMessage().GetProduct().PriceAmount1000
	if price == nil || *price != 0 {
		t.Fatalf("explicit zero price was not preserved: %#v", price)
	}
}

func TestBuildBusinessProductMessagePreservesExplicitZeroSalePrice(t *testing.T) {
	msg, err := BuildBusinessProductMessage(BusinessProductMessageParams{
		BusinessOwnerJID:    types.NewJID("15550001", types.DefaultUserServer),
		ProductID:           "p-sale",
		Title:               "Sale sample",
		CurrencyCode:        "USD",
		PriceAmount1000:     1000,
		SalePriceAmount1000: 0,
		SalePricePresent:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	salePrice := msg.GetProductMessage().GetProduct().SalePriceAmount1000
	if salePrice == nil || *salePrice != 0 {
		t.Fatalf("explicit zero sale price was not preserved: %#v", salePrice)
	}
}

func TestBuildBusinessProductListMessageMatchesWebGenerator(t *testing.T) {
	msg, err := BuildBusinessProductListMessage(BusinessProductListMessageParams{
		BusinessOwnerJID: types.NewJID("15550001", types.DefaultUserServer),
		Title:            "Seasonal", Description: "Choose a product", ButtonText: "View products", Footer: "Synthetic catalog",
		Sections: []BusinessProductSection{{Title: "Tea", ProductIDs: []string{"p-tea", "p-mint"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	list := msg.GetListMessage()
	if list.GetListType() != waE2E.ListMessage_PRODUCT_LIST || list.GetProductListInfo().GetBusinessOwnerJID() != "15550001@s.whatsapp.net" {
		t.Fatalf("unexpected list: %#v", list)
	}
	products := list.GetProductListInfo().GetProductSections()[0].GetProducts()
	if len(products) != 2 || products[1].GetProductID() != "p-mint" {
		t.Fatalf("unexpected products: %#v", products)
	}
}

func TestBuildBusinessOrderMessageMatchesWebGenerator(t *testing.T) {
	msg, err := BuildBusinessOrderMessage(BusinessOrderMessageParams{
		OrderID: "o-100", ItemCount: 2, Status: waE2E.OrderMessage_INQUIRY,
		Message: "Please review", OrderTitle: "Order o-100",
		SellerJID: types.NewJID("15550001", types.DefaultUserServer), Token: "synthetic-token",
		TotalAmount1000: 2650, TotalCurrencyCode: "USD", CatalogType: "regular", Thumbnail: []byte{1, 2, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	order := msg.GetOrderMessage()
	if order.GetOrderID() != "o-100" || order.GetSurface() != waE2E.OrderMessage_CATALOG || order.GetSellerJID() != "15550001@s.whatsapp.net" || order.GetTotalAmount1000() != 2650 {
		t.Fatalf("unexpected order: %#v", order)
	}
}

func TestBusinessProductListDescriptionAndOrderTokenAreOptional(t *testing.T) {
	owner := types.NewJID("15550001", types.DefaultUserServer)
	list, err := BuildBusinessProductListMessage(BusinessProductListMessageParams{
		BusinessOwnerJID: owner, Title: "Seasonal", ButtonText: "View products",
		Sections: []BusinessProductSection{{ProductIDs: []string{"p-tea"}}},
	})
	if err != nil {
		t.Fatalf("product list without description failed: %v", err)
	}
	if list.GetListMessage().Description != nil {
		t.Fatalf("omitted description was encoded: %q", list.GetListMessage().GetDescription())
	}

	order, err := BuildBusinessOrderMessage(BusinessOrderMessageParams{
		OrderID: "o-100", ItemCount: 1, Status: waE2E.OrderMessage_INQUIRY,
		SellerJID: owner, TotalCurrencyCode: "USD",
	})
	if err != nil {
		t.Fatalf("order without token failed: %v", err)
	}
	if order.GetOrderMessage().Token != nil {
		t.Fatalf("omitted token was encoded: %q", order.GetOrderMessage().GetToken())
	}
}

func TestBuildBusinessListAndNativeFlowButtonsMatchWebGenerators(t *testing.T) {
	list, err := BuildBusinessListMessage(BusinessListMessageParams{
		Title: "Support", Description: "Choose a topic", ButtonText: "View topics", Footer: "Synthetic support",
		Sections: []BusinessListSection{{Title: "Account", Rows: []BusinessListRow{{ID: "billing", Title: "Billing", Description: "Invoices and plans"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if list.GetListMessage().GetListType() != waE2E.ListMessage_SINGLE_SELECT || list.GetListMessage().GetSections()[0].GetRows()[0].GetRowID() != "billing" {
		t.Fatalf("unexpected single-select list: %#v", list.GetListMessage())
	}
	buttons, err := BuildBusinessNativeFlowButtonsMessage(BusinessNativeFlowButtonsMessageParams{
		Title: "Order help", Body: "Choose an action", Footer: "Synthetic support",
		Buttons: []BusinessNativeFlowButton{{Name: "cta_url", ParamsJSON: `{"display_text":"Track order","url":"https://synthetic.invalid/order/o-100"}`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	button := buttons.GetButtonsMessage().GetButtons()[0]
	if button.GetType() != waE2E.ButtonsMessage_Button_NATIVE_FLOW || button.GetNativeFlowInfo().GetName() != "cta_url" {
		t.Fatalf("unexpected native-flow button: %#v", button)
	}
}

func TestBusinessMessageBuildersNormalizeOwnerJIDs(t *testing.T) {
	deviceOwner := types.NewADJID("15550001", 0, 3)
	product, err := BuildBusinessProductMessage(BusinessProductMessageParams{
		BusinessOwnerJID: deviceOwner, ProductID: "p-tea", Title: "Tea", CurrencyCode: "USD", PriceAmount1000: 1250,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := product.GetProductMessage().GetBusinessOwnerJID(); got != deviceOwner.ToNonAD().String() {
		t.Fatalf("product owner = %q", got)
	}
	lidOwner := types.NewJID("123456789", types.HiddenUserServer)
	list, err := BuildBusinessProductListMessage(BusinessProductListMessageParams{
		BusinessOwnerJID: lidOwner, Title: "Products", Description: "Choose a product", ButtonText: "View",
		Sections: []BusinessProductSection{{ProductIDs: []string{"p-tea"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := list.GetListMessage().GetProductListInfo().GetBusinessOwnerJID(); got != lidOwner.String() {
		t.Fatalf("product list owner = %q", got)
	}
}

func TestBuildBusinessListRequiresBodyAndCapsRows(t *testing.T) {
	valid := BusinessListMessageParams{
		Description: "Choose a topic", ButtonText: "View topics",
		Sections: []BusinessListSection{{Rows: []BusinessListRow{{ID: "one", Title: "One"}}}},
	}
	if _, err := BuildBusinessListMessage(valid); err != nil {
		t.Fatalf("headerless list failed: %v", err)
	}
	missingBody := valid
	missingBody.Title = "Optional header"
	missingBody.Description = ""
	if _, err := BuildBusinessListMessage(missingBody); err == nil {
		t.Fatal("list without a body unexpectedly passed")
	}
	tooManyRows := valid
	tooManyRows.Sections[0].Rows = make([]BusinessListRow, 11)
	for index := range tooManyRows.Sections[0].Rows {
		tooManyRows.Sections[0].Rows[index] = BusinessListRow{ID: fmt.Sprintf("row-%d", index), Title: "Row"}
	}
	if _, err := BuildBusinessListMessage(tooManyRows); err == nil {
		t.Fatal("list with more than ten rows unexpectedly passed")
	}
}

func TestBusinessListBuildersRejectOversizedSectionsBeforeAllocating(t *testing.T) {
	owner := types.NewJID("15550001", types.DefaultUserServer)
	productIDs := make([]string, 1000)
	rows := make([]BusinessListRow, 1000)
	for index := range productIDs {
		productIDs[index] = fmt.Sprintf("product-%d", index)
		rows[index] = BusinessListRow{ID: fmt.Sprintf("row-%d", index), Title: "Row"}
	}

	productAllocs := testing.AllocsPerRun(1, func() {
		_, _ = BuildBusinessProductListMessage(BusinessProductListMessageParams{
			BusinessOwnerJID: owner,
			Title:            "Products",
			ButtonText:       "View",
			Sections:         []BusinessProductSection{{ProductIDs: productIDs}},
		})
	})
	if productAllocs > 50 {
		t.Fatalf("oversized product section allocated %.0f objects", productAllocs)
	}

	rowAllocs := testing.AllocsPerRun(1, func() {
		_, _ = BuildBusinessListMessage(BusinessListMessageParams{
			Description: "Choose a row",
			ButtonText:  "View",
			Sections:    []BusinessListSection{{Rows: rows}},
		})
	})
	if rowAllocs > 50 {
		t.Fatalf("oversized row section allocated %.0f objects", rowAllocs)
	}
}

func TestBuildBusinessAddressMessageMatchesWebGenerator(t *testing.T) {
	msg, err := BuildBusinessAddressMessage(BusinessAddressMessageParams{
		Body: "Where should we deliver?", ButtonText: "Share address", Footer: "Synthetic checkout",
		ContextInfo: &waE2E.ContextInfo{StanzaID: testPtr("quoted-message")},
	})
	if err != nil {
		t.Fatal(err)
	}
	interactive := msg.GetInteractiveMessage()
	flow := interactive.GetNativeFlowMessage()
	if interactive.GetBody().GetText() != "Where should we deliver?" || interactive.GetFooter().GetText() != "Synthetic checkout" {
		t.Fatalf("unexpected address envelope: %#v", interactive)
	}
	if len(flow.GetButtons()) != 1 || flow.GetButtons()[0].GetName() != "address_message" || flow.GetButtons()[0].GetButtonParamsJSON() != `{"display_text":"Share address"}` {
		t.Fatalf("unexpected address native flow: %#v", flow)
	}
	if flow.GetMessageVersion() != 1 || interactive.GetContextInfo().GetStanzaID() != "quoted-message" {
		t.Fatalf("unexpected address metadata: %#v", interactive)
	}
}

func TestBusinessAddressMessageEnforcesInteractiveTextLimits(t *testing.T) {
	valid := BusinessAddressMessageParams{Body: "Address", ButtonText: "Share", Footer: "Footer"}
	tests := map[string]BusinessAddressMessageParams{
		"body":        {Body: strings.Repeat("b", 1025), ButtonText: valid.ButtonText, Footer: valid.Footer},
		"button":      {Body: valid.Body, ButtonText: strings.Repeat("c", 21), Footer: valid.Footer},
		"button-utf8": {Body: valid.Body, ButtonText: string([]byte{0xff}), Footer: valid.Footer},
		"footer":      {Body: valid.Body, ButtonText: valid.ButtonText, Footer: strings.Repeat("f", 61)},
	}
	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildBusinessAddressMessage(params); err == nil {
				t.Fatal("expected address text limit error")
			}
		})
	}
}

func TestBusinessFlowMessageEnforcesInteractiveTextLimits(t *testing.T) {
	valid := BusinessFlowMessageParams{
		Body: "Book a visit", ButtonText: "Choose a time", Footer: "Appointments",
		FlowID: "flow-100", FlowToken: "synthetic-token", FlowAction: "navigate", Screen: "APPOINTMENT",
	}
	tests := map[string]BusinessFlowMessageParams{
		"body": {
			Body: strings.Repeat("b", 1025), ButtonText: valid.ButtonText, Footer: valid.Footer,
			FlowID: valid.FlowID, FlowToken: valid.FlowToken, FlowAction: valid.FlowAction, Screen: valid.Screen,
		},
		"button": {
			Body: valid.Body, ButtonText: strings.Repeat("c", 21), Footer: valid.Footer,
			FlowID: valid.FlowID, FlowToken: valid.FlowToken, FlowAction: valid.FlowAction, Screen: valid.Screen,
		},
		"button-utf8": {
			Body: valid.Body, ButtonText: string([]byte{0xff}), Footer: valid.Footer,
			FlowID: valid.FlowID, FlowToken: valid.FlowToken, FlowAction: valid.FlowAction, Screen: valid.Screen,
		},
		"footer": {
			Body: valid.Body, ButtonText: valid.ButtonText, Footer: strings.Repeat("f", 61),
			FlowID: valid.FlowID, FlowToken: valid.FlowToken, FlowAction: valid.FlowAction, Screen: valid.Screen,
		},
	}
	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildBusinessFlowMessage(params); err == nil {
				t.Fatal("expected flow text limit error")
			}
		})
	}
}

func TestBusinessFlowMessageRejectsInvalidUTF8PayloadFields(t *testing.T) {
	valid := BusinessFlowMessageParams{
		Body: "Book a visit", ButtonText: "Choose a time", FlowID: "flow-100", FlowToken: "synthetic-token",
		FlowAction: "navigate", Screen: "APPOINTMENT", DataJSON: `{"location":"beirut"}`,
	}
	tests := map[string]func(*BusinessFlowMessageParams){
		"flow-id":    func(params *BusinessFlowMessageParams) { params.FlowID = string([]byte{0xff}) },
		"flow-token": func(params *BusinessFlowMessageParams) { params.FlowToken = string([]byte{0xff}) },
		"screen":     func(params *BusinessFlowMessageParams) { params.Screen = string([]byte{0xff}) },
		"data": func(params *BusinessFlowMessageParams) {
			params.DataJSON = "{\"key\":\"" + string([]byte{0xff}) + "\"}"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			params := valid
			mutate(&params)
			if _, err := BuildBusinessFlowMessage(params); err == nil {
				t.Fatal("expected invalid UTF-8 error")
			}
		})
	}
}

func TestBuildBusinessFlowMessageMatchesWebGenerator(t *testing.T) {
	msg, err := BuildBusinessFlowMessage(BusinessFlowMessageParams{
		Body: "Book a visit", ButtonText: "Choose a time", FlowID: "flow-100", FlowToken: "synthetic-token",
		FlowAction: "navigate", Screen: "APPOINTMENT", DataJSON: `{"location":"beirut","order_id":9007199254740993}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow := msg.GetInteractiveMessage().GetNativeFlowMessage()
	if len(flow.GetButtons()) != 1 || flow.GetButtons()[0].GetName() != "galaxy_message" || flow.GetMessageVersion() != 1 {
		t.Fatalf("unexpected galaxy flow: %#v", flow)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(flow.GetButtons()[0].GetButtonParamsJSON()), &params); err != nil {
		t.Fatal(err)
	}
	if params["flow_message_version"] != "3" || params["flow_id"] != "flow-100" || params["flow_token"] != "synthetic-token" || params["flow_cta"] != "Choose a time" || params["flow_action"] != "navigate" {
		t.Fatalf("unexpected flow params: %#v", params)
	}
	payload := params["flow_action_payload"].(map[string]any)
	if payload["screen"] != "APPOINTMENT" || payload["data"].(map[string]any)["location"] != "beirut" {
		t.Fatalf("unexpected action payload: %#v", payload)
	}
	var exact struct {
		ActionPayload struct {
			Data map[string]json.RawMessage `json:"data"`
		} `json:"flow_action_payload"`
	}
	if err := json.Unmarshal([]byte(flow.GetButtons()[0].GetButtonParamsJSON()), &exact); err != nil {
		t.Fatal(err)
	}
	if string(exact.ActionPayload.Data["order_id"]) != "9007199254740993" {
		t.Fatalf("order ID lost precision: %s", exact.ActionPayload.Data["order_id"])
	}
}

func TestBuildBusinessFlowMessagePreservesExplicitEmptyData(t *testing.T) {
	base := BusinessFlowMessageParams{
		Body: "Book a visit", ButtonText: "Choose a time", FlowID: "flow-100", FlowToken: "synthetic-token",
		FlowAction: "navigate", Screen: "APPOINTMENT",
	}
	for name, dataJSON := range map[string]string{"omitted": "", "empty": `{}`} {
		t.Run(name, func(t *testing.T) {
			params := base
			params.DataJSON = dataJSON
			msg, err := BuildBusinessFlowMessage(params)
			if err != nil {
				t.Fatal(err)
			}
			var encoded struct {
				ActionPayload map[string]json.RawMessage `json:"flow_action_payload"`
			}
			buttonJSON := msg.GetInteractiveMessage().GetNativeFlowMessage().GetButtons()[0].GetButtonParamsJSON()
			if err := json.Unmarshal([]byte(buttonJSON), &encoded); err != nil {
				t.Fatal(err)
			}
			data, present := encoded.ActionPayload["data"]
			if dataJSON == "" && present {
				t.Fatalf("omitted data encoded as %s", data)
			}
			if dataJSON != "" && (!present || string(data) != `{}`) {
				t.Fatalf("explicit empty data encoded as %s", data)
			}
		})
	}
}

func TestBusinessMessageBuildersRejectUnsafeInputs(t *testing.T) {
	if _, err := BuildBusinessProductMessage(BusinessProductMessageParams{ProductID: "p", Title: "Tea", CurrencyCode: "USD"}); err == nil {
		t.Fatal("expected missing owner to fail")
	}
	owner := types.NewJID("15550001", types.DefaultUserServer)
	for name, params := range map[string]BusinessProductMessageParams{
		"non-HTTPS URL":      {BusinessOwnerJID: owner, ProductID: "p", Title: "Tea", CurrencyCode: "USD", URL: "http://synthetic.invalid/product"},
		"sale without price": {BusinessOwnerJID: owner, ProductID: "p", Title: "Tea", SalePriceAmount1000: 1000},
		"too many images":    {BusinessOwnerJID: owner, ProductID: "p", Title: "Tea", CurrencyCode: "USD", ProductImageCount: 11},
		"oversized body":     {BusinessOwnerJID: owner, ProductID: "p", Title: "Tea", Body: strings.Repeat("b", 1025)},
		"oversized footer":   {BusinessOwnerJID: owner, ProductID: "p", Title: "Tea", Footer: strings.Repeat("f", 61)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildBusinessProductMessage(params); err == nil {
				t.Fatal("expected product validation error")
			}
		})
	}
	if _, err := BuildBusinessProductMessage(BusinessProductMessageParams{
		BusinessOwnerJID: owner, ProductID: "p", Title: "Tea",
	}); err != nil {
		t.Fatalf("unpriced product was rejected: %v", err)
	}
	if _, err := BuildBusinessProductMessage(BusinessProductMessageParams{
		BusinessOwnerJID: types.NewJID("", types.DefaultUserServer), ProductID: "p", Title: "Tea",
	}); err == nil {
		t.Fatal("expected ownerless business JID to fail")
	}
	if _, err := BuildBusinessProductListMessage(BusinessProductListMessageParams{
		BusinessOwnerJID: types.NewJID("15550001", types.DefaultUserServer), Title: "Products", ButtonText: "View",
		Sections: []BusinessProductSection{{Title: "Tea", ProductIDs: []string{"p", "p"}}},
	}); err == nil {
		t.Fatal("expected duplicate product to fail")
	}
	if _, err := BuildBusinessOrderMessage(BusinessOrderMessageParams{
		OrderID: "o", ItemCount: 1, Status: waE2E.OrderMessage_INQUIRY,
		SellerJID: types.NewJID("15550001", types.DefaultUserServer), TotalAmount1000: -1, TotalCurrencyCode: "USD",
	}); err == nil {
		t.Fatal("expected negative total to fail")
	}
	if _, err := BuildBusinessOrderMessage(BusinessOrderMessageParams{
		OrderID: "o", ItemCount: 1, Status: waE2E.OrderMessage_INQUIRY,
		SellerJID: types.NewJID("15550001", types.DefaultUserServer), Token: " ", TotalCurrencyCode: "USD",
	}); err == nil {
		t.Fatal("expected blank order token to fail")
	}
	if _, err := BuildBusinessNativeFlowButtonsMessage(BusinessNativeFlowButtonsMessageParams{
		Body: "Choose", Buttons: []BusinessNativeFlowButton{{Name: "cta_url", ParamsJSON: "not-json"}},
	}); err == nil {
		t.Fatal("expected malformed native-flow parameters to fail")
	}
	if _, err := BuildBusinessAddressMessage(BusinessAddressMessageParams{Body: "Address", ButtonText: ""}); err == nil {
		t.Fatal("expected empty address CTA to fail")
	}
	if _, err := BuildBusinessFlowMessage(BusinessFlowMessageParams{
		Body: "Flow", ButtonText: "Open", FlowID: "flow", FlowToken: "token", FlowAction: "navigate", DataJSON: `[]`,
	}); err == nil {
		t.Fatal("expected non-object flow data to fail")
	}
	if _, err := BuildBusinessFlowMessage(BusinessFlowMessageParams{
		Body: "Flow", ButtonText: "Open", FlowID: "flow", FlowToken: "token", FlowAction: "navigate", Screen: "START", DataJSON: `{} {}`,
	}); err == nil {
		t.Fatal("expected trailing flow JSON to fail")
	}
}

func TestBusinessProductListAndNativeFlowTextLimits(t *testing.T) {
	owner := types.NewJID("15550001", types.DefaultUserServer)
	productList := BusinessProductListMessageParams{
		BusinessOwnerJID: owner, Title: "Products", Description: "Choose", ButtonText: "View", Footer: "Footer",
		Sections: []BusinessProductSection{{Title: "Section", ProductIDs: []string{"p"}}},
	}
	productMutations := map[string]func(*BusinessProductListMessageParams){
		"header":        func(params *BusinessProductListMessageParams) { params.Title = strings.Repeat("h", 61) },
		"body":          func(params *BusinessProductListMessageParams) { params.Description = strings.Repeat("b", 1025) },
		"button":        func(params *BusinessProductListMessageParams) { params.ButtonText = strings.Repeat("c", 21) },
		"footer":        func(params *BusinessProductListMessageParams) { params.Footer = strings.Repeat("f", 61) },
		"section title": func(params *BusinessProductListMessageParams) { params.Sections[0].Title = strings.Repeat("s", 25) },
	}
	for name, mutate := range productMutations {
		t.Run("product list "+name, func(t *testing.T) {
			params := productList
			params.Sections = append([]BusinessProductSection(nil), productList.Sections...)
			mutate(&params)
			if _, err := BuildBusinessProductListMessage(params); err == nil {
				t.Fatal("expected product-list protocol limit error")
			}
		})
	}
	multipleProductSections := productList
	multipleProductSections.Sections = []BusinessProductSection{
		{ProductIDs: []string{"one"}},
		{Title: "Second", ProductIDs: []string{"two"}},
	}
	if _, err := BuildBusinessProductListMessage(multipleProductSections); err == nil {
		t.Fatal("multiple product sections with an empty title unexpectedly passed")
	}

	nativeFlow := BusinessNativeFlowButtonsMessageParams{
		Title: "Title", Body: "Choose", Footer: "Footer",
		Buttons: []BusinessNativeFlowButton{{Name: "cta_url", ParamsJSON: `{}`}},
	}
	nativeMutations := map[string]func(*BusinessNativeFlowButtonsMessageParams){
		"header": func(params *BusinessNativeFlowButtonsMessageParams) { params.Title = strings.Repeat("h", 61) },
		"body":   func(params *BusinessNativeFlowButtonsMessageParams) { params.Body = strings.Repeat("b", 1025) },
		"footer": func(params *BusinessNativeFlowButtonsMessageParams) { params.Footer = strings.Repeat("f", 61) },
	}
	for name, mutate := range nativeMutations {
		t.Run("native flow "+name, func(t *testing.T) {
			params := nativeFlow
			mutate(&params)
			if _, err := BuildBusinessNativeFlowButtonsMessage(params); err == nil {
				t.Fatal("expected native-flow protocol limit error")
			}
		})
	}
}

func TestBusinessListMessageEnforcesProtocolTextLimits(t *testing.T) {
	valid := BusinessListMessageParams{
		Title: "Menu", Description: "Choose one", ButtonText: "Choose", Footer: "Footer",
		Sections: []BusinessListSection{{Title: "Section", Rows: []BusinessListRow{{ID: "one", Title: "One", Description: "Description"}}}},
	}
	mutations := map[string]func(*BusinessListMessageParams){
		"header":        func(params *BusinessListMessageParams) { params.Title = strings.Repeat("h", 61) },
		"body":          func(params *BusinessListMessageParams) { params.Description = strings.Repeat("b", 1025) },
		"button":        func(params *BusinessListMessageParams) { params.ButtonText = strings.Repeat("c", 21) },
		"footer":        func(params *BusinessListMessageParams) { params.Footer = strings.Repeat("f", 61) },
		"section title": func(params *BusinessListMessageParams) { params.Sections[0].Title = strings.Repeat("s", 25) },
		"row ID":        func(params *BusinessListMessageParams) { params.Sections[0].Rows[0].ID = strings.Repeat("i", 201) },
		"row title":     func(params *BusinessListMessageParams) { params.Sections[0].Rows[0].Title = strings.Repeat("r", 25) },
		"row description": func(params *BusinessListMessageParams) {
			params.Sections[0].Rows[0].Description = strings.Repeat("d", 73)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			params := valid
			params.Sections = []BusinessListSection{{Title: valid.Sections[0].Title, Rows: append([]BusinessListRow(nil), valid.Sections[0].Rows...)}}
			mutate(&params)
			if _, err := BuildBusinessListMessage(params); err == nil {
				t.Fatal("expected protocol limit error")
			}
		})
	}
	multipleSections := valid
	multipleSections.Sections = []BusinessListSection{
		{Rows: []BusinessListRow{{ID: "one", Title: "One"}}},
		{Title: "Second", Rows: []BusinessListRow{{ID: "two", Title: "Two"}}},
	}
	if _, err := BuildBusinessListMessage(multipleSections); err == nil {
		t.Fatal("multiple sections with an empty title unexpectedly passed")
	}
}

func testPtr[T any](value T) *T { return &value }

func syntheticProductInput() types.BusinessProductInput {
	return types.BusinessProductInput{
		Name:        "Mountain tea",
		Description: "Synthetic loose-leaf tea",
		Currency:    "USD",
		Price:       "12500",
		SalePrice:   "11000",
		URL:         "https://shop.example.test/tea",
		RetailerID:  "tea-001",
		ImageURLs:   []string{"https://mmg.whatsapp.net/product/tea-1", "https://mmg.whatsapp.net/product/tea-2"},
		VideoURLs:   []string{"https://mmg.whatsapp.net/product/tea-video"},
		Compliance: &types.BusinessComplianceInfo{
			CountryCodeOrigin: "LB",
			ImporterName:      "Synthetic Imports",
			ImporterAddress: &types.BusinessAddress{
				Street1: "1 Test Street", City: "Beirut", CountryCode: "LB",
			},
		},
	}
}

func TestBuildBusinessProductMutationVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	create, err := buildBusinessProductMutationVariables(jid, "", syntheticProductInput(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	product := create["input"].(map[string]any)["product"].(map[string]any)
	if product["biz_jid"] != jid.String() || product["width"] != 100 || product["height"] != 100 {
		t.Fatalf("unexpected create envelope: %#v", product)
	}
	if _, ok := product["product_id"]; ok {
		t.Fatal("create envelope unexpectedly contains product_id")
	}
	info := product["product_info"].(map[string]any)
	if info["name"] != "Mountain tea" || info["price"] != "12500" || info["sale_price"] != "11000" {
		t.Fatalf("unexpected product info: %#v", info)
	}
	media := info["media"].(map[string]any)
	images := media["image"].([]map[string]any)
	if len(images) != 2 || images[1]["url"] != "https://mmg.whatsapp.net/product/tea-2" {
		t.Fatalf("unexpected image input: %#v", images)
	}

	edit, err := buildBusinessProductMutationVariables(jid, "product-100", syntheticProductInput(), 320, 240)
	if err != nil {
		t.Fatal(err)
	}
	edited := edit["input"].(map[string]any)["product"].(map[string]any)
	if edited["product_id"] != "product-100" || edited["width"] != 320 || edited["height"] != 240 {
		t.Fatalf("unexpected edit envelope: %#v", edited)
	}
}

func TestBuildBusinessProductMutationVariablesRejectsUnsafeInput(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	tests := []types.BusinessProductInput{
		{},
		{Name: "Tea"},
		{Name: "Tea", ImageURLs: []string{"http://mmg.whatsapp.net/product/tea"}},
		{Name: "Tea", ImageURLs: []string{"https://example.test/product/tea"}},
		{Name: "Tea", ImageURLs: []string{"https://mmg.whatsapp.net/product/tea"}, Currency: "US", Price: "1250"},
		{Name: "Tea", ImageURLs: []string{"https://mmg.whatsapp.net/product/tea"}, Currency: "123", Price: "1250"},
		{Name: "Tea", ImageURLs: []string{"https://mmg.whatsapp.net/product/tea"}, Currency: "USD", Price: "12.50"},
		{Name: strings.Repeat("n", 257), ImageURLs: []string{"https://mmg.whatsapp.net/product/tea"}},
	}
	for i, input := range tests {
		if _, err := buildBusinessProductMutationVariables(jid, "", input, 100, 100); err == nil {
			t.Fatalf("case %d unexpectedly passed", i)
		}
	}
	if _, err := buildBusinessProductMutationVariables(jid, strings.Repeat("p", 257), syntheticProductInput(), 100, 100); err == nil {
		t.Fatal("oversized product ID unexpectedly passed")
	}
}

func TestBuildDeleteBusinessProductsVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	variables, err := buildDeleteBusinessProductsVariables(jid, []string{"product-100", "product-101"})
	if err != nil {
		t.Fatal(err)
	}
	input := variables["input"].(map[string]any)
	if input["biz_jid"] != jid.String() || len(input["product_ids"].([]string)) != 2 {
		t.Fatalf("unexpected delete variables: %#v", variables)
	}
	for _, ids := range [][]string{nil, {"same", "same"}, {strings.Repeat("p", 257)}} {
		if _, err = buildDeleteBusinessProductsVariables(jid, ids); err == nil {
			t.Fatalf("invalid IDs unexpectedly passed: %#v", ids)
		}
	}
}

func TestDecodeBusinessProductMutationResponses(t *testing.T) {
	productJSON := `{"id":"product-100","name":"Mountain tea","price":"12500","currency":"USD","media":{"images":[]},"status_info":{"status":"APPROVED"}}`
	created, err := decodeBusinessProductMutation(json.RawMessage(`{"xfb_whatsapp_catalog_add_product":{"product":`+productJSON+`}}`), "xfb_whatsapp_catalog_add_product")
	if err != nil || created.ID != "product-100" {
		t.Fatalf("created = %#v, error = %v", created, err)
	}
	updated, err := decodeBusinessProductMutation(json.RawMessage(`{"xfb_whatsapp_catalog_edit_product":{"product":`+productJSON+`}}`), "xfb_whatsapp_catalog_edit_product")
	if err != nil || updated.Name != "Mountain tea" {
		t.Fatalf("updated = %#v, error = %v", updated, err)
	}
	deleted, err := decodeDeleteBusinessProducts(json.RawMessage(`{"xfb_whatsapp_catalog_delete_product":{"deleted_count":2}}`))
	if err != nil || deleted != 2 {
		t.Fatalf("deleted = %d, error = %v", deleted, err)
	}
	if _, err = decodeBusinessProductMutation(json.RawMessage(`{"unexpected":{}}`), "xfb_whatsapp_catalog_add_product"); err == nil {
		t.Fatal("missing product discriminator unexpectedly passed")
	}
}

func TestBusinessCatalogAuthNodesAndResponse(t *testing.T) {
	nonceQuery := businessSilentNonceQuery()
	if nonceQuery.Namespace != "fb:thrift_iq" || nonceQuery.SMaxID != "118" || nonceQuery.Type != iqGet || nonceQuery.To != types.ServerJID {
		t.Fatalf("unexpected nonce query: %#v", nonceQuery)
	}
	exchange, err := businessTokenExchangeQuery("synthetic-nonce")
	if err != nil {
		t.Fatal(err)
	}
	parameters := exchange.Content.([]waBinary.Node)[0]
	code := parameters.Content.([]waBinary.Node)[0]
	if exchange.SMaxID != "104" || code.Tag != "code" || string(code.Content.([]byte)) != "synthetic-nonce" {
		t.Fatalf("unexpected exchange query: %#v", exchange)
	}
	response := waBinary.Node{Tag: "iq", Attrs: waBinary.Attrs{"type": "result"}, Content: []waBinary.Node{
		{Tag: "access_token", Content: []byte("synthetic-token")},
		{Tag: "session_cookies", Content: []byte("ignored")},
		{Tag: "business_person", Attrs: waBinary.Attrs{"id": "person-100"}},
		{Tag: "token_type", Content: []byte("Strong")},
	}}
	token, err := parseBusinessTokenResponse(&response)
	if err != nil || token.accessToken != "synthetic-token" || token.actorID != "person-100" {
		t.Fatalf("token = %#v, error = %v", token, err)
	}
	if _, err = businessTokenExchangeQuery(""); err == nil {
		t.Fatal("empty nonce unexpectedly passed")
	}
}

func TestHandleBusinessNonceNotificationIsLazyAndNonBlocking(t *testing.T) {
	client := &Client{}
	node := &waBinary.Node{Tag: "notification", Attrs: waBinary.Attrs{"type": "business"}, Content: []waBinary.Node{{Tag: "wa_ad_account_nonce", Content: []byte("unused")}}}
	client.handleBusinessCatalogNotification(node)
	if client.businessCatalogAuth.Load() != nil {
		t.Fatal("unsolicited nonce allocated catalog auth state")
	}
	state := client.getBusinessCatalogAuth()
	waiter := &businessNonceWaiter{ch: make(chan string, 1)}
	state.nonceWaiter.Store(waiter)
	client.handleBusinessCatalogNotification(node)
	select {
	case nonce := <-waiter.ch:
		if nonce != "unused" {
			t.Fatalf("nonce = %q", nonce)
		}
	default:
		t.Fatal("nonce was not delivered")
	}
}

func TestBusinessNonceDeliveredBeforeHandlerQueue(t *testing.T) {
	client := &Client{handlerQueue: make(chan *waBinary.Node, 1)}
	client.handlerQueue <- &waBinary.Node{Tag: "message"}
	state := client.getBusinessCatalogAuth()
	waiter := &businessNonceWaiter{ch: make(chan string, 1)}
	state.nonceWaiter.Store(waiter)
	node := &waBinary.Node{Tag: "notification", Attrs: waBinary.Attrs{"type": "business"}, Content: []waBinary.Node{{Tag: "wa_ad_account_nonce", Content: []byte("synthetic-nonce")}}}

	client.handleOutOfBandNode(node)
	select {
	case nonce := <-waiter.ch:
		if nonce != "synthetic-nonce" {
			t.Fatalf("nonce = %q", nonce)
		}
	default:
		t.Fatal("nonce was blocked behind the handler queue")
	}
	if len(client.handlerQueue) != 1 {
		t.Fatalf("out-of-band delivery changed handler queue length to %d", len(client.handlerQueue))
	}
}

func TestBusinessNonceIsNotRedeliveredFromHandlerQueue(t *testing.T) {
	client := &Client{}
	state := client.getBusinessCatalogAuth()
	firstWaiter := &businessNonceWaiter{ch: make(chan string, 1)}
	state.nonceWaiter.Store(firstWaiter)
	node := &waBinary.Node{Tag: "notification", Attrs: waBinary.Attrs{"type": "business"}, Content: []waBinary.Node{{Tag: "wa_ad_account_nonce", Content: []byte("stale-nonce")}}}
	client.handleOutOfBandNode(node)
	<-firstWaiter.ch

	secondWaiter := &businessNonceWaiter{ch: make(chan string, 1)}
	state.nonceWaiter.Store(secondWaiter)
	client.handleQueuedBusinessCatalogNotification(node)
	select {
	case nonce := <-secondWaiter.ch:
		t.Fatalf("queued handler redelivered stale nonce %q", nonce)
	default:
	}
}

func TestBusinessAccessTokenLockObservesCancellation(t *testing.T) {
	client := &Client{}
	state := client.getBusinessCatalogAuth()
	<-state.tokenLock
	defer func() { state.tokenLock <- struct{}{} }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.businessAccessToken(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled token waiter remained blocked")
	}
}

func TestBusinessAccessTokenInvalidationObservesCancellation(t *testing.T) {
	client := &Client{}
	state := client.getBusinessCatalogAuth()
	<-state.tokenLock
	defer func() { state.tokenLock <- struct{}{} }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.invalidateBusinessAccessToken(ctx, "synthetic-token"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestExecuteBusinessProductMutationUsesCurrentActorID(t *testing.T) {
	client := &Client{}
	state := client.getBusinessCatalogAuth()
	<-state.tokenLock
	state.token = businessAccessToken{accessToken: "old-token", actorID: "actor-old"}
	state.tokenLock <- struct{}{}

	var actors []string
	var tokens []string
	requests := 0
	client.mediaHTTP = &http.Client{Transport: businessProductRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		var body struct {
			AccessToken string         `json:"access_token"`
			Variables   map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		input := body.Variables["input"].(map[string]any)
		actors = append(actors, input["actor_id"].(string))
		tokens = append(tokens, body.AccessToken)
		if requests == 1 {
			<-state.tokenLock
			state.token = businessAccessToken{accessToken: "new-token", actorID: "actor-new"}
			state.tokenLock <- struct{}{}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"errors":[{"code":190}]}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":{"ok":true}}`)),
		}, nil
	})}
	variables := map[string]any{"input": map[string]any{"product": map[string]any{"name": "Tea"}}}
	if _, err := client.executeBusinessCatalogMutation(context.Background(), businessAddProductDocumentID, variables); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(actors, []string{"actor-old", "actor-new"}) || !slices.Equal(tokens, []string{"old-token", "new-token"}) {
		t.Fatalf("actors = %v, tokens = %v", actors, tokens)
	}
	if _, exists := variables["input"].(map[string]any)["actor_id"]; exists {
		t.Fatal("mutation variables were modified in place")
	}
}

func TestSendBusinessFacebookGraphQL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %s %#v", r.Method, r.Header)
		}
		var body struct {
			AccessToken string         `json:"access_token"`
			DocumentID  string         `json:"doc_id"`
			Locale      string         `json:"locale"`
			Variables   map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.AccessToken != "synthetic-token" || body.DocumentID != businessAddProductDocumentID || body.Locale != "en_US" || body.Variables["input"] == nil {
			t.Fatalf("unexpected GraphQL body: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"xfb_whatsapp_catalog_add_product":{"product":{"id":"product-100"}}}}`)
	}))
	defer server.Close()
	client := &Client{mediaHTTP: server.Client()}
	data, err := client.sendBusinessFacebookGraphQL(context.Background(), server.URL, businessAddProductDocumentID, "synthetic-token", map[string]any{"input": map[string]any{"product": map[string]any{"name": "Tea"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("product-100")) {
		t.Fatalf("unexpected data: %s", data)
	}
}

func TestSendBusinessFacebookGraphQLClassifiesAuthErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errors":[{"code":190,"message":"expired"}]}`)
	}))
	defer server.Close()
	client := &Client{mediaHTTP: server.Client()}
	_, err := client.sendBusinessFacebookGraphQL(context.Background(), server.URL, businessAddProductDocumentID, "synthetic-token", map[string]any{"input": map[string]any{}})
	if err == nil || !isBusinessGraphQLAuthError(err) || strings.Contains(err.Error(), "synthetic-token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendBusinessFacebookGraphQLClassifiesHTTPAuthErrorsWithoutJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client := &Client{mediaHTTP: server.Client()}
	_, err := client.sendBusinessFacebookGraphQL(context.Background(), server.URL, businessAddProductDocumentID, "synthetic-token", map[string]any{"input": map[string]any{}})
	if err == nil || !isBusinessGraphQLAuthError(err) || strings.Contains(err.Error(), "decode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUploadBusinessProductImageUsesPlaintextProductPath(t *testing.T) {
	image := append([]byte("\x89PNG\r\n\x1a\n"), []byte("synthetic-product-image")...)
	hash := sha256.Sum256(image)
	token := base64.URLEncoding.EncodeToString(hash[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/product/image/"+token || r.URL.Query().Get("auth") != "synthetic-auth" {
			t.Fatalf("unexpected upload URL: %s", r.URL.RequestURI())
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, image) {
			t.Fatalf("body mismatch: %v", err)
		}
		_, _ = io.WriteString(w, `{"direct_path":"/product/tea"}`)
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		mediaHTTP:      server.Client(),
		mediaConnCache: &MediaConn{Auth: "synthetic-auth", TTL: 3600, FetchedAt: time.Now(), Hosts: []MediaConnHost{{Hostname: serverURL.Host}}},
	}
	got, err := client.UploadBusinessProductImage(context.Background(), image)
	if err != nil || got != "https://mmg.whatsapp.net/product/tea" {
		t.Fatalf("URL = %q, error = %v", got, err)
	}
}

type businessProductRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip businessProductRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestUploadBusinessProductImageRedactsTransportURL(t *testing.T) {
	sentinel := errors.New("synthetic transport failure")
	client := &Client{
		mediaHTTP: &http.Client{Transport: businessProductRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, sentinel
		})},
		mediaConnCache: &MediaConn{
			Auth: "sensitive-auth", TTL: 3600, FetchedAt: time.Now(), Hosts: []MediaConnHost{{Hostname: "upload.invalid"}},
		},
	}
	image := append([]byte("\x89PNG\r\n\x1a\n"), []byte("synthetic-product-image")...)
	_, err := client.UploadBusinessProductImage(context.Background(), image)
	if !errors.Is(err, sentinel) {
		t.Fatalf("transport cause was not preserved: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive-auth") {
		t.Fatalf("transport error exposed auth query: %v", err)
	}
}

func TestUploadBusinessProductImageRedactsRequestConstructionURL(t *testing.T) {
	client := &Client{
		mediaConnCache: &MediaConn{
			Auth: "sensitive-auth", TTL: 3600, FetchedAt: time.Now(), Hosts: []MediaConnHost{{Hostname: "[invalid"}},
		},
	}
	image := append([]byte("\x89PNG\r\n\x1a\n"), []byte("synthetic-product-image")...)
	_, err := client.UploadBusinessProductImage(context.Background(), image)
	if err == nil {
		t.Fatal("malformed upload host unexpectedly passed")
	}
	if strings.Contains(err.Error(), "sensitive-auth") {
		t.Fatalf("request construction error exposed auth query: %v", err)
	}
}

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

func TestBuildBusinessProfileDeltaClearsHours(t *testing.T) {
	hours := types.BusinessHoursUpdate{TimeZone: "UTC"}
	node, err := buildBusinessProfileDelta(types.BusinessProfileUpdate{Hours: &hours})
	if err != nil {
		t.Fatal(err)
	}
	hoursNode := node.GetChildByTag("business_hours")
	if hoursNode.AttrGetter().String("timezone") != "UTC" || len(hoursNode.GetChildren()) != 0 {
		t.Fatalf("unexpected business hours removal node: %#v", hoursNode)
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

type businessCoverRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip businessCoverRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestUploadBusinessCoverPhotoRedactsTransportURL(t *testing.T) {
	sentinel := errors.New("synthetic transport failure")
	client := &Client{
		mediaHTTP: &http.Client{Transport: businessCoverRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, sentinel
		})},
		mediaConnCache: &MediaConn{
			Auth: "sensitive-auth", TTL: 3600, FetchedAt: time.Now(), Hosts: []MediaConnHost{{Hostname: "upload.invalid"}},
		},
	}
	image := append([]byte("\x89PNG\r\n\x1a\n"), []byte("synthetic-image")...)
	_, err := client.uploadBusinessCoverPhoto(context.Background(), image)
	if !errors.Is(err, sentinel) {
		t.Fatalf("transport cause was not preserved: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive-auth") {
		t.Fatalf("transport error exposed auth query: %v", err)
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
	setDelta := buildBusinessProfileMutationNode(setNode)
	setChildren := setDelta.GetChildren()
	if setDelta.Tag != "business_profile" || setDelta.AttrGetter().String("mutation_type") != "delta" || len(setChildren) != 1 || setChildren[0].Tag != "cover_photo" {
		t.Fatalf("unexpected set delta: %#v", setDelta)
	}
	deleteNode, err := buildBusinessCoverPhotoDeleteNode("cover-100")
	if err != nil {
		t.Fatal(err)
	}
	if deleteNode.AttrGetter().String("op") != "delete" || deleteNode.AttrGetter().String("id") != "cover-100" {
		t.Fatalf("unexpected delete node: %#v", deleteNode)
	}
	deleteDelta := buildBusinessProfileMutationNode(deleteNode)
	deleteChildren := deleteDelta.GetChildren()
	if deleteDelta.Tag != "business_profile" || len(deleteChildren) != 1 || deleteChildren[0].Tag != "cover_photo" {
		t.Fatalf("unexpected delete delta: %#v", deleteDelta)
	}
	if _, err = buildBusinessCoverPhotoDeleteNode(""); err == nil {
		t.Fatal("expected empty cover ID error")
	}
}
