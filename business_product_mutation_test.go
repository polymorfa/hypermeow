package whatsmeow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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
