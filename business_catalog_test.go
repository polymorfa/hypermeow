package whatsmeow

import (
	"encoding/json"
	"strings"
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
)

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
