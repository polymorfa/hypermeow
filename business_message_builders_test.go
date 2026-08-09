package whatsmeow

import (
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

func TestBusinessMessageBuildersRejectUnsafeInputs(t *testing.T) {
	if _, err := BuildBusinessProductMessage(BusinessProductMessageParams{ProductID: "p", Title: "Tea", CurrencyCode: "USD"}); err == nil {
		t.Fatal("expected missing owner to fail")
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
	if _, err := BuildBusinessNativeFlowButtonsMessage(BusinessNativeFlowButtonsMessageParams{
		Body: "Choose", Buttons: []BusinessNativeFlowButton{{Name: "cta_url", ParamsJSON: "not-json"}},
	}); err == nil {
		t.Fatal("expected malformed native-flow parameters to fail")
	}
}

func testPtr[T any](value T) *T { return &value }
