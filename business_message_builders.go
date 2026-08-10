package whatsmeow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

type BusinessProductMessageParams struct {
	BusinessOwnerJID    types.JID
	ProductID           string
	Title               string
	Description         string
	CurrencyCode        string
	PriceAmount1000     int64
	SalePriceAmount1000 int64
	SalePricePresent    bool
	RetailerID          string
	URL                 string
	ProductImageCount   uint32
	ProductImage        *waE2E.ImageMessage
	Body                string
	Footer              string
	ContextInfo         *waE2E.ContextInfo
}

type BusinessProductSection struct {
	Title      string
	ProductIDs []string
}

type BusinessProductListMessageParams struct {
	BusinessOwnerJID types.JID
	Title            string
	Description      string
	ButtonText       string
	Footer           string
	Sections         []BusinessProductSection
	ContextInfo      *waE2E.ContextInfo
}

type BusinessOrderMessageParams struct {
	OrderID           string
	Thumbnail         []byte
	ItemCount         int32
	Status            waE2E.OrderMessage_OrderStatus
	Message           string
	OrderTitle        string
	SellerJID         types.JID
	Token             string
	TotalAmount1000   int64
	TotalCurrencyCode string
	CatalogType       string
	ContextInfo       *waE2E.ContextInfo
}

type BusinessListRow struct {
	ID          string
	Title       string
	Description string
}

type BusinessListSection struct {
	Title string
	Rows  []BusinessListRow
}

type BusinessListMessageParams struct {
	Title       string
	Description string
	ButtonText  string
	Footer      string
	Sections    []BusinessListSection
	ContextInfo *waE2E.ContextInfo
}

type BusinessNativeFlowButton struct {
	Name       string
	ParamsJSON string
}

type BusinessNativeFlowButtonsMessageParams struct {
	Title       string
	Body        string
	Footer      string
	Buttons     []BusinessNativeFlowButton
	ContextInfo *waE2E.ContextInfo
}

type BusinessAddressMessageParams struct {
	Body        string
	ButtonText  string
	Footer      string
	ContextInfo *waE2E.ContextInfo
}

type BusinessFlowMessageParams struct {
	Body        string
	ButtonText  string
	Footer      string
	FlowID      string
	FlowToken   string
	FlowAction  string
	Screen      string
	DataJSON    string
	ContextInfo *waE2E.ContextInfo
}

func validBusinessOwner(jid types.JID) bool {
	return !jid.IsEmpty() && jid.User != "" && (jid.Server == types.DefaultUserServer || jid.Server == types.HiddenUserServer)
}

func validCurrency(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, char := range code {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func bounded(value string, max int) bool {
	return len(value) <= max
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return proto.String(value)
}

func optionalPositiveInt64(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return proto.Int64(value)
}

func optionalPositiveUint32(value uint32) *uint32 {
	if value == 0 {
		return nil
	}
	return proto.Uint32(value)
}

func BuildBusinessProductMessage(params BusinessProductMessageParams) (*waE2E.Message, error) {
	if !validBusinessOwner(params.BusinessOwnerJID) {
		return nil, errors.New("invalid business owner JID")
	}
	if strings.TrimSpace(params.ProductID) == "" || !bounded(params.ProductID, 256) || strings.TrimSpace(params.Title) == "" || !bounded(params.Title, 256) {
		return nil, errors.New("invalid business product identity")
	}
	if !bounded(params.Description, 4096) || !bounded(params.RetailerID, 256) || !bounded(params.URL, 2048) || !bounded(params.Body, 1024) || !bounded(params.Footer, 60) {
		return nil, errors.New("business product message field is too large")
	}
	if params.PriceAmount1000 < 0 || params.SalePriceAmount1000 < 0 {
		return nil, errors.New("invalid business product price")
	}
	pricePresent := params.PriceAmount1000 != 0 || params.CurrencyCode != ""
	if !pricePresent && (params.SalePriceAmount1000 > 0 || params.SalePricePresent) {
		return nil, errors.New("business product sale price requires a base price")
	}
	if pricePresent && !validCurrency(params.CurrencyCode) {
		return nil, errors.New("invalid business product currency")
	}
	if params.ProductImageCount > 10 {
		return nil, errors.New("business product cannot contain more than 10 images")
	}
	if params.URL != "" {
		parsed, err := url.ParseRequestURI(params.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
			return nil, errors.New("business product URL must be absolute HTTPS")
		}
	}
	priceAmount1000 := optionalPositiveInt64(params.PriceAmount1000)
	if pricePresent {
		priceAmount1000 = proto.Int64(params.PriceAmount1000)
	}
	salePriceAmount1000 := optionalPositiveInt64(params.SalePriceAmount1000)
	if params.SalePricePresent {
		salePriceAmount1000 = proto.Int64(params.SalePriceAmount1000)
	}
	return &waE2E.Message{ProductMessage: &waE2E.ProductMessage{
		Product: &waE2E.ProductMessage_ProductSnapshot{
			ProductImage: params.ProductImage, ProductID: proto.String(params.ProductID), Title: proto.String(params.Title),
			Description: optionalString(params.Description), CurrencyCode: optionalString(params.CurrencyCode),
			PriceAmount1000: priceAmount1000, SalePriceAmount1000: salePriceAmount1000,
			RetailerID: optionalString(params.RetailerID), URL: optionalString(params.URL), ProductImageCount: optionalPositiveUint32(params.ProductImageCount),
		},
		BusinessOwnerJID: proto.String(params.BusinessOwnerJID.ToNonAD().String()), Body: optionalString(params.Body), Footer: optionalString(params.Footer), ContextInfo: params.ContextInfo,
	}}, nil
}

func BuildBusinessProductListMessage(params BusinessProductListMessageParams) (*waE2E.Message, error) {
	if !validBusinessOwner(params.BusinessOwnerJID) {
		return nil, errors.New("invalid business owner JID")
	}
	if strings.TrimSpace(params.Title) == "" || !bounded(params.Title, 60) || !bounded(params.Description, 1024) || strings.TrimSpace(params.ButtonText) == "" || !bounded(params.ButtonText, 20) || !bounded(params.Footer, 60) {
		return nil, errors.New("invalid business product list text")
	}
	if len(params.Sections) == 0 || len(params.Sections) > 10 {
		return nil, errors.New("business product list must contain 1 to 10 sections")
	}
	sections := make([]*waE2E.ListMessage_ProductSection, len(params.Sections))
	seen := make(map[string]struct{})
	productCount := 0
	for index, section := range params.Sections {
		if !bounded(section.Title, 24) || len(section.ProductIDs) == 0 || (len(params.Sections) > 1 && strings.TrimSpace(section.Title) == "") {
			return nil, fmt.Errorf("invalid business product section %d", index)
		}
		if len(section.ProductIDs) > 30-productCount {
			return nil, errors.New("business product list exceeds 30 products")
		}
		productCount += len(section.ProductIDs)
		products := make([]*waE2E.ListMessage_Product, len(section.ProductIDs))
		for productIndex, productID := range section.ProductIDs {
			if strings.TrimSpace(productID) == "" || !bounded(productID, 256) {
				return nil, fmt.Errorf("invalid product ID in section %d", index)
			}
			if _, exists := seen[productID]; exists {
				return nil, fmt.Errorf("duplicate product ID %q", productID)
			}
			seen[productID] = struct{}{}
			products[productIndex] = &waE2E.ListMessage_Product{ProductID: proto.String(productID)}
		}
		sections[index] = &waE2E.ListMessage_ProductSection{Title: optionalString(section.Title), Products: products}
	}
	return &waE2E.Message{ListMessage: &waE2E.ListMessage{
		Title: proto.String(params.Title), Description: optionalString(params.Description), ButtonText: proto.String(params.ButtonText),
		ListType: waE2E.ListMessage_PRODUCT_LIST.Enum(), FooterText: optionalString(params.Footer),
		ProductListInfo: &waE2E.ListMessage_ProductListInfo{ProductSections: sections, BusinessOwnerJID: proto.String(params.BusinessOwnerJID.ToNonAD().String())}, ContextInfo: params.ContextInfo,
	}}, nil
}

func BuildBusinessOrderMessage(params BusinessOrderMessageParams) (*waE2E.Message, error) {
	if !validBusinessOwner(params.SellerJID) {
		return nil, errors.New("invalid seller JID")
	}
	if strings.TrimSpace(params.OrderID) == "" || !bounded(params.OrderID, 256) || (params.Token != "" && strings.TrimSpace(params.Token) == "") || params.ItemCount < 1 || params.ItemCount > 100 {
		return nil, errors.New("invalid business order identity")
	}
	if params.Status < waE2E.OrderMessage_INQUIRY || params.Status > waE2E.OrderMessage_DECLINED || params.TotalAmount1000 < 0 || !validCurrency(params.TotalCurrencyCode) {
		return nil, errors.New("invalid business order state")
	}
	if len(params.Thumbnail) > 64*1024 || !bounded(params.Message, 4096) || !bounded(params.OrderTitle, 256) || !bounded(params.Token, 8192) || !bounded(params.CatalogType, 128) {
		return nil, errors.New("business order message field is too large")
	}
	return &waE2E.Message{OrderMessage: &waE2E.OrderMessage{
		OrderID: proto.String(params.OrderID), Thumbnail: params.Thumbnail, ItemCount: proto.Int32(params.ItemCount),
		Status: params.Status.Enum(), Surface: waE2E.OrderMessage_CATALOG.Enum(), Message: optionalString(params.Message),
		OrderTitle: optionalString(params.OrderTitle), SellerJID: proto.String(params.SellerJID.ToNonAD().String()), Token: optionalString(params.Token),
		TotalAmount1000: proto.Int64(params.TotalAmount1000), TotalCurrencyCode: proto.String(params.TotalCurrencyCode), CatalogType: optionalString(params.CatalogType), ContextInfo: params.ContextInfo,
	}}, nil
}

func BuildBusinessListMessage(params BusinessListMessageParams) (*waE2E.Message, error) {
	if !bounded(params.Title, 60) || strings.TrimSpace(params.Description) == "" || !bounded(params.Description, 1024) || strings.TrimSpace(params.ButtonText) == "" || !bounded(params.ButtonText, 20) || !bounded(params.Footer, 60) {
		return nil, errors.New("invalid business list text")
	}
	if len(params.Sections) == 0 || len(params.Sections) > 10 {
		return nil, errors.New("business list must contain 1 to 10 sections")
	}
	sections := make([]*waE2E.ListMessage_Section, len(params.Sections))
	seen := make(map[string]struct{})
	rowCount := 0
	for sectionIndex, section := range params.Sections {
		if !bounded(section.Title, 24) || len(section.Rows) == 0 || (len(params.Sections) > 1 && strings.TrimSpace(section.Title) == "") {
			return nil, fmt.Errorf("invalid business list section %d", sectionIndex)
		}
		if len(section.Rows) > 10-rowCount {
			return nil, errors.New("business list exceeds 10 rows")
		}
		rowCount += len(section.Rows)
		rows := make([]*waE2E.ListMessage_Row, len(section.Rows))
		for rowIndex, row := range section.Rows {
			if strings.TrimSpace(row.ID) == "" || !bounded(row.ID, 200) || strings.TrimSpace(row.Title) == "" || !bounded(row.Title, 24) || !bounded(row.Description, 72) {
				return nil, fmt.Errorf("invalid business list row %d in section %d", rowIndex, sectionIndex)
			}
			if _, exists := seen[row.ID]; exists {
				return nil, fmt.Errorf("duplicate business list row ID %q", row.ID)
			}
			seen[row.ID] = struct{}{}
			rows[rowIndex] = &waE2E.ListMessage_Row{RowID: proto.String(row.ID), Title: proto.String(row.Title), Description: optionalString(row.Description)}
		}
		sections[sectionIndex] = &waE2E.ListMessage_Section{Title: optionalString(section.Title), Rows: rows}
	}
	return &waE2E.Message{ListMessage: &waE2E.ListMessage{
		Title: proto.String(params.Title), Description: optionalString(params.Description), ButtonText: proto.String(params.ButtonText),
		ListType: waE2E.ListMessage_SINGLE_SELECT.Enum(), Sections: sections, FooterText: optionalString(params.Footer), ContextInfo: params.ContextInfo,
	}}, nil
}

func BuildBusinessNativeFlowButtonsMessage(params BusinessNativeFlowButtonsMessageParams) (*waE2E.Message, error) {
	if strings.TrimSpace(params.Body) == "" || !bounded(params.Body, 1024) || !bounded(params.Title, 60) || !bounded(params.Footer, 60) {
		return nil, errors.New("invalid business native-flow text")
	}
	if len(params.Buttons) == 0 || len(params.Buttons) > 3 {
		return nil, errors.New("business native-flow message must contain 1 to 3 buttons")
	}
	buttons := make([]*waE2E.ButtonsMessage_Button, len(params.Buttons))
	for index, button := range params.Buttons {
		if strings.TrimSpace(button.Name) == "" || !bounded(button.Name, 64) || strings.TrimSpace(button.ParamsJSON) == "" || !bounded(button.ParamsJSON, 8192) {
			return nil, fmt.Errorf("invalid business native-flow button %d", index)
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(button.ParamsJSON), &object); err != nil || object == nil {
			return nil, fmt.Errorf("invalid business native-flow params for button %d", index)
		}
		buttons[index] = &waE2E.ButtonsMessage_Button{
			Type: waE2E.ButtonsMessage_Button_NATIVE_FLOW.Enum(),
			NativeFlowInfo: &waE2E.ButtonsMessage_Button_NativeFlowInfo{
				Name: proto.String(button.Name), ParamsJSON: proto.String(button.ParamsJSON),
			},
		}
	}
	headerType := waE2E.ButtonsMessage_EMPTY
	message := &waE2E.ButtonsMessage{
		ContentText: proto.String(params.Body), FooterText: optionalString(params.Footer), Buttons: buttons, HeaderType: headerType.Enum(), ContextInfo: params.ContextInfo,
	}
	if params.Title != "" {
		headerType = waE2E.ButtonsMessage_TEXT
		message.HeaderType = headerType.Enum()
		message.Header = &waE2E.ButtonsMessage_Text{Text: params.Title}
	}
	return &waE2E.Message{ButtonsMessage: message}, nil
}

func BuildBusinessAddressMessage(params BusinessAddressMessageParams) (*waE2E.Message, error) {
	if strings.TrimSpace(params.Body) == "" || !bounded(params.Body, 1024) || strings.TrimSpace(params.ButtonText) == "" || !bounded(params.ButtonText, 20) || !bounded(params.Footer, 60) {
		return nil, errors.New("invalid business address message text")
	}
	buttonParams, err := json.Marshal(struct {
		DisplayText string `json:"display_text"`
	}{DisplayText: params.ButtonText})
	if err != nil {
		return nil, fmt.Errorf("marshal business address message: %w", err)
	}
	return buildBusinessInteractiveNativeFlow(params.Body, params.Footer, "address_message", string(buttonParams), params.ContextInfo), nil
}

func BuildBusinessFlowMessage(params BusinessFlowMessageParams) (*waE2E.Message, error) {
	if strings.TrimSpace(params.Body) == "" || !bounded(params.Body, 4096) || strings.TrimSpace(params.ButtonText) == "" || !bounded(params.ButtonText, 256) || !bounded(params.Footer, 256) {
		return nil, errors.New("invalid business flow message text")
	}
	if strings.TrimSpace(params.FlowID) == "" || !bounded(params.FlowID, 256) || strings.TrimSpace(params.FlowToken) == "" || !bounded(params.FlowToken, 8192) {
		return nil, errors.New("invalid business flow identity")
	}
	if params.FlowAction != "navigate" && params.FlowAction != "data_exchange" {
		return nil, errors.New("invalid business flow action")
	}
	if !bounded(params.Screen, 256) || (params.FlowAction == "navigate" && strings.TrimSpace(params.Screen) == "") {
		return nil, errors.New("invalid business flow screen")
	}
	if params.FlowAction == "data_exchange" && (params.Screen != "" || params.DataJSON != "") {
		return nil, errors.New("data-exchange flow messages cannot include an action payload")
	}
	if !bounded(params.DataJSON, 16*1024) {
		return nil, errors.New("business flow data is too large")
	}
	var data map[string]json.RawMessage
	if params.DataJSON != "" {
		if err := json.Unmarshal([]byte(params.DataJSON), &data); err != nil || data == nil {
			return nil, errors.New("business flow data must be a JSON object")
		}
	}
	type actionPayload struct {
		Screen string                     `json:"screen,omitempty"`
		Data   map[string]json.RawMessage `json:"data,omitempty"`
	}
	var payload *actionPayload
	if params.FlowAction == "navigate" {
		payload = &actionPayload{Screen: params.Screen, Data: data}
	}
	buttonParams, err := json.Marshal(struct {
		Version       string         `json:"flow_message_version"`
		Token         string         `json:"flow_token"`
		ID            string         `json:"flow_id"`
		CTA           string         `json:"flow_cta"`
		Action        string         `json:"flow_action"`
		ActionPayload *actionPayload `json:"flow_action_payload,omitempty"`
	}{
		Version: "3", Token: params.FlowToken, ID: params.FlowID, CTA: params.ButtonText, Action: params.FlowAction,
		ActionPayload: payload,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal business flow message: %w", err)
	}
	return buildBusinessInteractiveNativeFlow(params.Body, params.Footer, "galaxy_message", string(buttonParams), params.ContextInfo), nil
}

func buildBusinessInteractiveNativeFlow(body, footer, name, buttonParams string, contextInfo *waE2E.ContextInfo) *waE2E.Message {
	interactive := &waE2E.InteractiveMessage{
		Body:        &waE2E.InteractiveMessage_Body{Text: proto.String(body)},
		ContextInfo: contextInfo,
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
			Buttons:        []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{{Name: proto.String(name), ButtonParamsJSON: proto.String(buttonParams)}},
			MessageVersion: proto.Int32(1),
		}},
	}
	if footer != "" {
		interactive.Footer = &waE2E.InteractiveMessage_Footer{Text: proto.String(footer)}
	}
	return &waE2E.Message{InteractiveMessage: interactive}
}
