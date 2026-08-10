package whatsmeow

import (
	"fmt"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

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

func TestBusinessMessageBuildersRejectUnsafeInputs(t *testing.T) {
	if _, err := BuildBusinessProductMessage(BusinessProductMessageParams{ProductID: "p", Title: "Tea", CurrencyCode: "USD"}); err == nil {
		t.Fatal("expected missing owner to fail")
	}
	owner := types.NewJID("15550001", types.DefaultUserServer)
	for name, params := range map[string]BusinessProductMessageParams{
		"non-HTTPS URL":      {BusinessOwnerJID: owner, ProductID: "p", Title: "Tea", CurrencyCode: "USD", URL: "http://synthetic.invalid/product"},
		"sale without price": {BusinessOwnerJID: owner, ProductID: "p", Title: "Tea", SalePriceAmount1000: 1000},
		"too many images":    {BusinessOwnerJID: owner, ProductID: "p", Title: "Tea", CurrencyCode: "USD", ProductImageCount: 11},
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
