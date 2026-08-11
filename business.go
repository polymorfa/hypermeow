// Copyright (c) 2025 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

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
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/mex"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/socket"
	"github.com/polymorfa/hypermeow/types"
)

// GetOrderDetails fetches the details of a specific order using its ID and token.
// Both token and orderID are found in the OrderMessage.
func (cli *Client) GetOrderDetails(ctx context.Context, orderID, tokenBase64 string) (*types.OrderDetails, error) {
	if err := validateOrderLookup(orderID, tokenBase64); err != nil {
		return nil, err
	}
	resp, err := cli.sendIQ(ctx, infoQuery{
		Namespace: "fb:thrift_iq",
		Type:      iqGet,
		SMaxID:    "5",
		To:        types.ServerJID,
		Content: []waBinary.Node{{
			Tag: "order",
			Attrs: waBinary.Attrs{
				"op": "get",
				"id": orderID,
			},
			Content: []waBinary.Node{
				{
					Tag: "image_dimensions",
					Content: []waBinary.Node{
						{Tag: "width", Content: []byte("100")},
						{Tag: "height", Content: []byte("100")},
					},
				},
				{Tag: "token", Content: []byte(tokenBase64)},
			},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to send order IQ: %w", err)
	}

	orderNode, ok := resp.GetOptionalChildByTag("order")
	if !ok {
		return nil, &ElementMissingError{Tag: "order", In: "response to order query"}
	}

	details, err := parseOrderDetailsNode(orderNode)
	if err != nil {
		return nil, err
	}
	if err = validateOrderResponseID(orderID, details.ID); err != nil {
		return nil, err
	}
	return details, nil
}

func validateOrderLookup(orderID, token string) error {
	if strings.TrimSpace(orderID) == "" {
		return fmt.Errorf("order ID is empty")
	}
	if len(orderID) > 256 {
		return fmt.Errorf("order ID exceeds 256 bytes")
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("order token is empty")
	}
	if len(token) > 8192 {
		return fmt.Errorf("order token exceeds 8192 bytes")
	}
	return nil
}

func validateOrderResponseID(requested, returned string) error {
	if returned != requested {
		return fmt.Errorf("order response ID %q does not match requested ID %q", returned, requested)
	}
	return nil
}

// Helper to get the string content of a child node.
func getStringChild(node waBinary.Node, tag string) string {
	child, ok := node.GetOptionalChildByTag(tag)
	if !ok {
		return ""
	}
	content, _ := child.Content.([]byte)
	return string(content)
}

func parseOrderDetailsNode(orderNode waBinary.Node) (*types.OrderDetails, error) {
	ag := orderNode.AttrGetter()
	details := &types.OrderDetails{
		ID:        ag.String("id"),
		CreatedAt: ag.UnixTime("creation_ts"),
	}
	if err := ag.Error(); err != nil {
		return nil, err
	}

	priceNode, ok := orderNode.GetOptionalChildByTag("price")
	if ok {
		subtotal, err := parseInt64Child(priceNode, "subtotal")
		if err != nil {
			return nil, err
		}
		total, err := parseInt64Child(priceNode, "total")
		if err != nil {
			return nil, err
		}
		details.Price = types.OrderPrice{
			Subtotal:    subtotal,
			Total:       total,
			Currency:    getStringChild(priceNode, "currency"),
			PriceStatus: getStringChild(priceNode, "price_status"),
		}
	}

	catalogNode, ok := orderNode.GetOptionalChildByTag("catalog")
	if ok {
		details.CatalogID = getStringChild(catalogNode, "id")
	}

	for _, productNode := range orderNode.GetChildrenByTag("product") {
		price, err := parseInt64Child(productNode, "price")
		if err != nil {
			return nil, err
		}
		quantity, err := parseIntChild(productNode, "quantity")
		if err != nil {
			return nil, err
		}

		product := types.OrderProduct{
			ID:       getStringChild(productNode, "id"),
			Price:    price,
			Currency: getStringChild(productNode, "currency"),
			Name:     getStringChild(productNode, "name"),
			Quantity: quantity,
		}

		if imageNode, ok := productNode.GetOptionalChildByTag("image"); ok {
			product.ImageID = getStringChild(imageNode, "id")
			product.ImageURL = getStringChild(imageNode, "url")
		}

		if variantNode, ok := productNode.GetOptionalChildByTag("variant_info"); ok {
			product.VariantInfo.Properties = getStringChild(variantNode, "properties")
		}

		details.Products = append(details.Products, product)
	}

	return details, nil
}

func parseInt64Child(node waBinary.Node, tag string) (int64, error) {
	raw := getStringChild(node, tag)
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", tag, raw, err)
	}
	return value, nil
}

func parseIntChild(node waBinary.Node, tag string) (int, error) {
	raw := getStringChild(node, tag)
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", tag, raw, err)
	}
	return value, nil
}

const (
	maxBusinessAccountIDBytes         = 256
	maxBusinessAccountNameBytes       = 512
	maxBusinessAccountURLBytes        = 4096
	maxBusinessEligibilityParamsBytes = 16 * 1024
)

var businessEligibilityFeatures = []types.BusinessFeature{
	types.BusinessFeatureMetaVerified,
	types.BusinessFeatureMarketingMessages,
	types.BusinessFeatureGenAI,
	types.BusinessFeatureGenAIImage,
	types.BusinessFeatureMetaOne,
	types.BusinessFeatureBBPro,
}

func businessLinkedAccountsQuery() infoQuery {
	return infoQuery{
		Namespace: "fb:thrift_iq",
		Type:      iqGet,
		To:        types.ServerJID,
		SMaxID:    "42",
		Content:   []waBinary.Node{{Tag: "linked_accounts"}},
	}
}

func businessEligibilityQuery(features []types.BusinessFeature) (infoQuery, error) {
	if len(features) == 0 {
		features = businessEligibilityFeatures
	}
	attrs := make(waBinary.Attrs, len(features))
	for _, feature := range features {
		if !isBusinessEligibilityFeature(feature) {
			return infoQuery{}, fmt.Errorf("unknown business feature %q", feature)
		}
		if _, exists := attrs[string(feature)]; exists {
			return infoQuery{}, fmt.Errorf("duplicate business feature %q", feature)
		}
		attrs[string(feature)] = "true"
	}
	return infoQuery{
		Namespace: "w:biz",
		Type:      iqGet,
		To:        types.ServerJID,
		SMaxID:    "139",
		Content:   []waBinary.Node{{Tag: "features", Attrs: attrs}},
	}, nil
}

func isBusinessEligibilityFeature(feature types.BusinessFeature) bool {
	for _, known := range businessEligibilityFeatures {
		if feature == known {
			return true
		}
	}
	return false
}

func (cli *Client) GetBusinessLinkedAccounts(ctx context.Context) (*types.BusinessLinkedAccounts, error) {
	response, err := cli.sendIQ(ctx, businessLinkedAccountsQuery())
	if err != nil {
		return nil, fmt.Errorf("get linked business accounts: %w", err)
	}
	return parseBusinessLinkedAccounts(response)
}

func (cli *Client) GetBusinessEligibility(ctx context.Context, features ...types.BusinessFeature) (*types.BusinessEligibility, error) {
	query, err := businessEligibilityQuery(features)
	if err != nil {
		return nil, err
	}
	response, err := cli.sendIQ(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get business eligibility: %w", err)
	}
	return parseBusinessEligibility(response)
}

func parseBusinessLinkedAccounts(response *waBinary.Node) (*types.BusinessLinkedAccounts, error) {
	root, ok := response.GetOptionalChildByTag("linked_accounts")
	if !ok {
		return nil, &ElementMissingError{Tag: "linked_accounts", In: "business linked accounts response"}
	}
	result := &types.BusinessLinkedAccounts{}
	for _, node := range root.GetChildren() {
		var err error
		switch node.Tag {
		case "fb_page":
			result.FacebookPage, err = parseBusinessFacebookPage(node)
		case "fb_biz":
			result.FacebookBusiness, err = parseBusinessFacebookBusiness(node)
		case "ig_professional":
			result.InstagramProfessional, err = parseBusinessInstagram(node)
		case "whatsapp_ad_identity":
			result.WhatsAppAdIdentity, err = parseBusinessWhatsAppAdIdentity(node)
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func parseBusinessFacebookPage(node waBinary.Node) (*types.BusinessFacebookPage, error) {
	attrs := node.AttrGetter()
	page := &types.BusinessFacebookPage{ID: attrs.String("id")}
	if err := attrs.Error(); err != nil {
		return nil, fmt.Errorf("parse Facebook Page: %w", err)
	}
	if err := validateBusinessAccountText("Facebook Page ID", page.ID, maxBusinessAccountIDBytes); err != nil {
		return nil, err
	}
	var err error
	if page.DisplayName, err = requiredBusinessNodeText(node, "display_name", maxBusinessAccountNameBytes); err != nil {
		return nil, err
	}
	if page.ProfilePictureURL, err = requiredBusinessPictureURL(node); err != nil {
		return nil, err
	}
	if page.ShowOnProfile, err = requiredBusinessNodeBool(node, "show_on_profile"); err != nil {
		return nil, err
	}
	if sync, ok := node.GetOptionalChildByTag("profile_sync"); ok {
		page.ProfileSync, err = requiredBusinessEnumAttr(sync, "state", "disable", "import")
		if err != nil {
			return nil, err
		}
	}
	if page.HasActiveCTWAAd, page.HasCreatedAd, err = requiredBusinessAdStatus(node); err != nil {
		return nil, err
	}
	button, ok := node.GetOptionalChildByTag("whatsapp_as_page_button")
	if !ok {
		return nil, &ElementMissingError{Tag: "whatsapp_as_page_button", In: "Facebook Page"}
	}
	state, err := requiredBusinessEnumAttr(button, "state", "off", "on")
	if err != nil {
		return nil, err
	}
	page.WhatsAppAsPageButton = state == "on"
	return page, nil
}

func parseBusinessFacebookBusiness(node waBinary.Node) (*types.BusinessFacebookBusiness, error) {
	attrs := node.AttrGetter()
	business := &types.BusinessFacebookBusiness{ID: attrs.String("id")}
	if err := attrs.Error(); err != nil {
		return nil, fmt.Errorf("parse Facebook business: %w", err)
	}
	if err := validateBusinessAccountText("Facebook business ID", business.ID, maxBusinessAccountIDBytes); err != nil {
		return nil, err
	}
	var err error
	if business.DisplayName, err = requiredBusinessNodeText(node, "display_name", maxBusinessAccountNameBytes); err != nil {
		return nil, err
	}
	if catalog, ok := node.GetOptionalChildByTag("catalog"); ok {
		catalogAttrs := catalog.AttrGetter()
		business.CatalogID = catalogAttrs.String("id")
		business.CatalogState = catalogAttrs.String("state")
		if err = catalogAttrs.Error(); err != nil {
			return nil, fmt.Errorf("parse linked catalog: %w", err)
		}
		if err = validateBusinessAccountText("catalog ID", business.CatalogID, maxBusinessAccountIDBytes); err != nil {
			return nil, err
		}
		if business.CatalogState != "disable" && business.CatalogState != "import" {
			return nil, fmt.Errorf("invalid catalog state %q", business.CatalogState)
		}
	}
	return business, nil
}

func parseBusinessInstagram(node waBinary.Node) (*types.BusinessInstagramProfessional, error) {
	instagram := &types.BusinessInstagramProfessional{}
	var err error
	if instagram.Handle, err = requiredBusinessNodeText(node, "ig_handle", maxBusinessAccountNameBytes); err != nil {
		return nil, err
	}
	if instagram.DisplayName, err = requiredBusinessNodeText(node, "display_name", maxBusinessAccountNameBytes); err != nil {
		return nil, err
	}
	if instagram.ProfilePictureURL, err = requiredBusinessPictureURL(node); err != nil {
		return nil, err
	}
	if instagram.ShowOnProfile, err = requiredBusinessNodeBool(node, "show_on_profile"); err != nil {
		return nil, err
	}
	return instagram, nil
}

func parseBusinessWhatsAppAdIdentity(node waBinary.Node) (*types.BusinessWhatsAppAdIdentity, error) {
	attrs := node.AttrGetter()
	identity := &types.BusinessWhatsAppAdIdentity{ID: attrs.String("id")}
	if err := attrs.Error(); err != nil {
		return nil, fmt.Errorf("parse WhatsApp ad identity: %w", err)
	}
	if err := validateBusinessAccountText("WhatsApp ad identity ID", identity.ID, maxBusinessAccountIDBytes); err != nil {
		return nil, err
	}
	var err error
	identity.HasActiveCTWAAd, identity.HasCreatedAd, err = requiredBusinessAdStatus(node)
	if err != nil {
		return nil, err
	}
	return identity, nil
}

func requiredBusinessAdStatus(node waBinary.Node) (bool, bool, error) {
	status, ok := node.GetOptionalChildByTag("ad_status")
	if !ok {
		return false, false, &ElementMissingError{Tag: "ad_status", In: node.Tag}
	}
	attrs := status.AttrGetter()
	active := attrs.Bool("has_active_ctwa_ad")
	created := attrs.Bool("has_created_ad")
	if err := attrs.Error(); err != nil {
		return false, false, fmt.Errorf("parse %s ad status: %w", node.Tag, err)
	}
	return active, created, nil
}

func requiredBusinessPictureURL(node waBinary.Node) (string, error) {
	picture, ok := node.GetOptionalChildByTag("profile_picture")
	if !ok {
		return "", &ElementMissingError{Tag: "profile_picture", In: node.Tag}
	}
	return requiredBusinessNodeText(picture, "url", maxBusinessAccountURLBytes)
}

func requiredBusinessNodeText(node waBinary.Node, tag string, maxBytes int) (string, error) {
	child, ok := node.GetOptionalChildByTag(tag)
	if !ok {
		return "", &ElementMissingError{Tag: tag, In: node.Tag}
	}
	content, ok := child.Content.([]byte)
	if !ok {
		return "", fmt.Errorf("%s in %s has invalid content type %T", tag, node.Tag, child.Content)
	}
	value := string(content)
	if err := validateBusinessAccountText(tag, value, maxBytes); err != nil {
		return "", err
	}
	return value, nil
}

func requiredBusinessNodeBool(node waBinary.Node, tag string) (bool, error) {
	value, err := requiredBusinessNodeText(node, tag, 5)
	if err != nil {
		return false, err
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s value %q: %w", tag, value, err)
	}
	return parsed, nil
}

func requiredBusinessEnumAttr(node waBinary.Node, attr string, allowed ...string) (string, error) {
	attrs := node.AttrGetter()
	value := attrs.String(attr)
	if err := attrs.Error(); err != nil {
		return "", fmt.Errorf("parse %s: %w", node.Tag, err)
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", fmt.Errorf("invalid %s %s %q", node.Tag, attr, value)
}

func validateBusinessAccountText(field, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("%s is empty", field)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", field, maxBytes)
	}
	return nil
}

func parseBusinessEligibility(response *waBinary.Node) (*types.BusinessEligibility, error) {
	result := &types.BusinessEligibility{Features: make([]types.BusinessFeatureEligibility, 0, len(businessEligibilityFeatures))}
	for _, node := range response.GetChildren() {
		feature := types.BusinessFeature(node.Tag)
		if !isBusinessEligibilityFeature(feature) {
			continue
		}
		attrs := node.AttrGetter()
		entry := types.BusinessFeatureEligibility{Feature: feature, Status: attrs.String("status")}
		if expiration, ok := attrs.GetInt64("expiration", false); ok {
			entry.Expiration = expiration
		}
		entry.AdditionalParams = attrs.OptionalString("additional_params")
		if value, ok := attrs.GetBool("should_show_privacy_interstitial_to_new_users", false); ok {
			entry.ShowPrivacyInterstitial = &value
		}
		if value, ok := attrs.GetBool("v1_enabled", false); ok {
			entry.V1Enabled = &value
		}
		if err := attrs.Error(); err != nil {
			return nil, fmt.Errorf("parse %s eligibility: %w", feature, err)
		}
		if err := validateBusinessEligibilityStatus(feature, entry.Status); err != nil {
			return nil, err
		}
		if len(entry.AdditionalParams) > maxBusinessEligibilityParamsBytes {
			return nil, fmt.Errorf("%s additional_params exceeds %d bytes", feature, maxBusinessEligibilityParamsBytes)
		}
		result.Features = append(result.Features, entry)
	}
	return result, nil
}

func validateBusinessEligibilityStatus(feature types.BusinessFeature, status string) error {
	var allowed []string
	switch feature {
	case types.BusinessFeatureMarketingMessages:
		allowed = []string{"FAIL", "PAUSED", "SUCCESS", "WARNING"}
	case types.BusinessFeatureBBPro:
		allowed = []string{"ELIGIBLE_TO_ONBOARD", "NOT_ELIGIBLE", "ONBOARDED"}
	default:
		allowed = []string{"FAIL", "SUCCESS"}
	}
	for _, candidate := range allowed {
		if status == candidate {
			return nil
		}
	}
	return fmt.Errorf("invalid %s eligibility status %q", feature, status)
}

type GetCatalogParams struct {
	After  string
	Limit  int
	Width  int
	Height int
}

type GetCollectionsParams struct {
	After           string
	CollectionLimit int
	ItemLimit       int
	Width           int
	Height          int
}

func decodeCatalogPage(data json.RawMessage) (*types.BusinessCatalogPage, error) {
	var response struct {
		Catalog *struct {
			ProductCatalog *struct {
				Paging *struct {
					After  string `json:"after"`
					Before string `json:"before"`
				} `json:"paging"`
				Products []types.BusinessProduct `json:"products"`
			} `json:"product_catalog"`
		} `json:"xwa_product_catalog_get_product_catalog"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode catalog response: %w", err)
	}
	if response.Catalog == nil || response.Catalog.ProductCatalog == nil {
		return nil, fmt.Errorf("catalog response is missing xwa_product_catalog_get_product_catalog.product_catalog")
	}
	page := &types.BusinessCatalogPage{Products: response.Catalog.ProductCatalog.Products}
	if page.Products == nil {
		page.Products = []types.BusinessProduct{}
	}
	if response.Catalog.ProductCatalog.Paging != nil {
		page.Next = response.Catalog.ProductCatalog.Paging.After
		page.Previous = response.Catalog.ProductCatalog.Paging.Before
	}
	return page, nil
}

func buildCatalogVariables(jid types.JID, params GetCatalogParams) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if len(params.After) > 2048 {
		return nil, fmt.Errorf("catalog cursor exceeds 2048 bytes")
	}
	if params.Limit == 0 {
		params.Limit = 50
	}
	if params.Limit < 1 || params.Limit > 100 {
		return nil, fmt.Errorf("catalog limit must be between 1 and 100")
	}
	width, height, err := normalizeDimensions(params.Width, params.Height)
	if err != nil {
		return nil, err
	}

	request := map[string]any{
		"jid":                      jid.ToNonAD().String(),
		"limit":                    strconv.Itoa(params.Limit),
		"width":                    strconv.Itoa(width),
		"height":                   strconv.Itoa(height),
		"variant_thumbnail_width":  strconv.Itoa(width),
		"variant_thumbnail_height": strconv.Itoa(height),
		"variant_info_fields":      map[string]any{},
		"allow_shop_source":        "ALLOWSHOPSOURCE_FALSE",
	}
	if params.After != "" {
		request["after"] = params.After
	}
	return map[string]any{"request": map[string]any{"product_catalog": request}}, nil
}

func validateBusinessJID(jid types.JID) error {
	if jid.IsEmpty() || jid.User == "" {
		return fmt.Errorf("business JID is empty")
	}
	if jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer {
		return fmt.Errorf("business JID must be a user or LID JID")
	}
	return nil
}

func buildCatalogProductVariables(jid types.JID, productID string, width, height int) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if err := validateBusinessID("product", productID); err != nil {
		return nil, err
	}
	width, height, err := normalizeDimensions(width, height)
	if err != nil {
		return nil, err
	}
	return map[string]any{"request": map[string]any{"product": map[string]any{
		"jid":                      jid.ToNonAD().String(),
		"product_id":               productID,
		"width":                    strconv.Itoa(width),
		"height":                   strconv.Itoa(height),
		"variant_thumbnail_width":  strconv.Itoa(width),
		"variant_thumbnail_height": strconv.Itoa(height),
		"variant_info_fields":      map[string]any{},
		"fetch_compliance_info":    "true",
	}}}, nil
}

func buildCollectionsVariables(jid types.JID, params GetCollectionsParams) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if len(params.After) > 2048 {
		return nil, fmt.Errorf("collection cursor exceeds 2048 bytes")
	}
	if params.CollectionLimit == 0 {
		params.CollectionLimit = 20
	}
	if params.CollectionLimit < 1 || params.CollectionLimit > 20 {
		return nil, fmt.Errorf("collection limit must be between 1 and 20")
	}
	if params.ItemLimit == 0 {
		params.ItemLimit = 50
	}
	if params.ItemLimit < 1 || params.ItemLimit > 100 {
		return nil, fmt.Errorf("collection item limit must be between 1 and 100")
	}
	width, height, err := normalizeDimensions(params.Width, params.Height)
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"biz_jid":                  jid.ToNonAD().String(),
		"collection_limit":         strconv.Itoa(params.CollectionLimit),
		"item_limit":               strconv.Itoa(params.ItemLimit),
		"width":                    strconv.Itoa(width),
		"height":                   strconv.Itoa(height),
		"variant_thumbnail_width":  strconv.Itoa(width),
		"variant_thumbnail_height": strconv.Itoa(height),
		"variant_info_fields":      map[string]any{},
	}
	if params.After != "" {
		request["after"] = params.After
	}
	return map[string]any{"request": map[string]any{"collections": request}}, nil
}

func buildSingleCollectionVariables(jid types.JID, collectionID string, params GetCatalogParams) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if err := validateBusinessID("collection", collectionID); err != nil {
		return nil, err
	}
	if len(params.After) > 2048 {
		return nil, fmt.Errorf("collection cursor exceeds 2048 bytes")
	}
	if params.Limit == 0 {
		params.Limit = 50
	}
	if params.Limit < 1 || params.Limit > 100 {
		return nil, fmt.Errorf("collection item limit must be between 1 and 100")
	}
	width, height, err := normalizeDimensions(params.Width, params.Height)
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"biz_jid":                  jid.ToNonAD().String(),
		"id":                       collectionID,
		"limit":                    strconv.Itoa(params.Limit),
		"width":                    strconv.Itoa(width),
		"height":                   strconv.Itoa(height),
		"variant_thumbnail_width":  strconv.Itoa(width),
		"variant_thumbnail_height": strconv.Itoa(height),
		"variant_info_fields":      map[string]any{},
	}
	if params.After != "" {
		request["after"] = params.After
	}
	return map[string]any{"request": map[string]any{"collection": request}}, nil
}

func buildProductListVariables(jid types.JID, productIDs []string, width, height int) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if len(productIDs) < 1 || len(productIDs) > 100 {
		return nil, fmt.Errorf("product list must contain between 1 and 100 IDs")
	}
	products := make([]map[string]any, len(productIDs))
	seen := make(map[string]struct{}, len(productIDs))
	for i, id := range productIDs {
		if err := validateBusinessID("product", id); err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate product ID %q", id)
		}
		seen[id] = struct{}{}
		products[i] = map[string]any{"id": id}
	}
	width, height, err := normalizeDimensions(width, height)
	if err != nil {
		return nil, err
	}
	return map[string]any{"request": map[string]any{"product_list": map[string]any{
		"jid":      jid.ToNonAD().String(),
		"products": products,
		"width":    strconv.Itoa(width),
		"height":   strconv.Itoa(height),
	}}}, nil
}

func normalizeDimensions(width, height int) (int, int, error) {
	if width == 0 {
		width = 100
	}
	if height == 0 {
		height = 100
	}
	if width < 1 || width > 1024 || height < 1 || height > 1024 {
		return 0, 0, fmt.Errorf("catalog image dimensions must be between 1 and 1024")
	}
	return width, height, nil
}

func validateBusinessID(kind, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%s ID is empty", kind)
	}
	if len(id) > 256 {
		return fmt.Errorf("%s ID exceeds 256 bytes", kind)
	}
	return nil
}

func decodeCatalogProduct(data json.RawMessage) (*types.BusinessProduct, error) {
	var response struct {
		Result *struct {
			Catalog *struct {
				Product *types.BusinessProduct `json:"product"`
			} `json:"product_catalog"`
		} `json:"xwa_product_catalog_get_product"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode catalog product response: %w", err)
	}
	if response.Result == nil || response.Result.Catalog == nil || response.Result.Catalog.Product == nil {
		return nil, fmt.Errorf("catalog product response is missing xwa_product_catalog_get_product.product_catalog.product")
	}
	return response.Result.Catalog.Product, nil
}

func decodeCollections(data json.RawMessage) (*types.BusinessCollectionPage, error) {
	var response struct {
		Result *struct {
			Collections []types.BusinessCollection `json:"collections"`
			Paging      *struct {
				After string `json:"after"`
			} `json:"paging"`
		} `json:"xwa_product_catalog_get_collections"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode collections response: %w", err)
	}
	if response.Result == nil {
		return nil, fmt.Errorf("collections response is missing xwa_product_catalog_get_collections")
	}
	page := &types.BusinessCollectionPage{Collections: response.Result.Collections}
	if page.Collections == nil {
		page.Collections = []types.BusinessCollection{}
	}
	if response.Result.Paging != nil {
		page.Next = response.Result.Paging.After
	}
	return page, nil
}

func decodeSingleCollection(data json.RawMessage) (*types.BusinessCollection, error) {
	var response struct {
		Result *struct {
			Collection *types.BusinessCollection `json:"collection"`
			Paging     *struct {
				After  string `json:"after"`
				Before string `json:"before"`
			} `json:"paging"`
		} `json:"xwa_product_catalog_get_single_collection"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode collection response: %w", err)
	}
	if response.Result == nil || response.Result.Collection == nil {
		return nil, fmt.Errorf("collection response is missing xwa_product_catalog_get_single_collection.collection")
	}
	if response.Result.Collection.Products == nil {
		response.Result.Collection.Products = []types.BusinessProduct{}
	}
	if response.Result.Paging != nil {
		response.Result.Collection.Next = response.Result.Paging.After
		response.Result.Collection.Previous = response.Result.Paging.Before
	}
	return response.Result.Collection, nil
}

func decodeProductList(data json.RawMessage, requested []string) ([]types.BusinessProduct, error) {
	var response struct {
		Result *struct {
			List *struct {
				Products []types.BusinessProduct `json:"products"`
			} `json:"product_list"`
		} `json:"xwa_product_catalog_get_product_list"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode product list response: %w", err)
	}
	if response.Result == nil || response.Result.List == nil {
		return nil, fmt.Errorf("product list response is missing xwa_product_catalog_get_product_list.product_list")
	}
	byID := make(map[string]types.BusinessProduct, len(response.Result.List.Products))
	for _, product := range response.Result.List.Products {
		if product.ID == "" {
			return nil, fmt.Errorf("product list response contains an empty product ID")
		}
		if _, exists := byID[product.ID]; exists {
			return nil, fmt.Errorf("product list response contains duplicate product ID %q", product.ID)
		}
		byID[product.ID] = product
	}
	products := make([]types.BusinessProduct, len(requested))
	for i, id := range requested {
		product, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("product list response is missing requested product %q", id)
		}
		products[i] = product
	}
	return products, nil
}

func (cli *Client) GetCatalog(ctx context.Context, business types.JID, params GetCatalogParams) (*types.BusinessCatalogPage, error) {
	variables, err := buildCatalogVariables(business, params)
	if err != nil {
		return nil, err
	}
	data, err := cli.sendBusinessMex(ctx, mex.QueryCatalog, variables)
	if err != nil {
		return nil, err
	}
	return decodeCatalogPage(data)
}

func (cli *Client) GetCatalogProduct(ctx context.Context, business types.JID, productID string) (*types.BusinessProduct, error) {
	variables, err := buildCatalogProductVariables(business, productID, 100, 100)
	if err != nil {
		return nil, err
	}
	data, err := cli.sendBusinessMex(ctx, mex.QueryCatalogProduct, variables)
	if err != nil {
		return nil, err
	}
	return decodeCatalogProduct(data)
}

func (cli *Client) GetProductCollections(ctx context.Context, business types.JID, params GetCollectionsParams) (*types.BusinessCollectionPage, error) {
	variables, err := buildCollectionsVariables(business, params)
	if err != nil {
		return nil, err
	}
	data, err := cli.sendBusinessMex(ctx, mex.QueryProductCollections, variables)
	if err != nil {
		return nil, err
	}
	return decodeCollections(data)
}

func (cli *Client) GetProductCollection(ctx context.Context, business types.JID, collectionID string, params GetCatalogParams) (*types.BusinessCollection, error) {
	variables, err := buildSingleCollectionVariables(business, collectionID, params)
	if err != nil {
		return nil, err
	}
	data, err := cli.sendBusinessMex(ctx, mex.QueryProductSingleCollection, variables)
	if err != nil {
		return nil, err
	}
	return decodeSingleCollection(data)
}

func (cli *Client) GetCatalogProducts(ctx context.Context, business types.JID, productIDs []string) ([]types.BusinessProduct, error) {
	variables, err := buildProductListVariables(business, productIDs, 100, 100)
	if err != nil {
		return nil, err
	}
	data, err := cli.sendBusinessMex(ctx, mex.QueryProductListCatalog, variables)
	if err != nil {
		return nil, err
	}
	return decodeProductList(data, productIDs)
}

func (cli *Client) sendBusinessMex(ctx context.Context, operationName mex.OperationName, variables map[string]any) (json.RawMessage, error) {
	operation, ok := mex.Lookup(operationName)
	if !ok {
		return nil, fmt.Errorf("business MEX operation %q is not pinned", operationName)
	}
	data, err := cli.sendMexIQ(ctx, operation.DocumentID, variables)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operationName, err)
	}
	return data, nil
}

const (
	businessCreateCollectionDocumentID   = "29361942130088470"
	businessDeleteCollectionsDocumentID  = "29970196299234260"
	businessUpdateCollectionDocumentID   = "24486970300891371"
	businessReorderCollectionsDocumentID = "9930298893688430"
	maxBusinessCollectionItems           = 100
	maxBusinessCollectionMoves           = 100
)

func validateBusinessCollectionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("business collection name is empty")
	}
	if len(name) > 256 {
		return "", fmt.Errorf("business collection name exceeds 256 bytes")
	}
	return name, nil
}

func validateBusinessCollectionProductIDs(productIDs []string, allowEmpty bool) error {
	if (!allowEmpty && len(productIDs) == 0) || len(productIDs) > maxBusinessCollectionItems {
		return fmt.Errorf("business collection product list must contain between 1 and %d IDs", maxBusinessCollectionItems)
	}
	seen := make(map[string]struct{}, len(productIDs))
	for _, productID := range productIDs {
		if err := validateBusinessID("product", productID); err != nil {
			return err
		}
		if _, exists := seen[productID]; exists {
			return fmt.Errorf("duplicate product ID %q", productID)
		}
		seen[productID] = struct{}{}
	}
	return nil
}

func buildCreateBusinessCollectionVariables(jid types.JID, name string, productIDs []string, catalogSessionID string) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	name, err := validateBusinessCollectionName(name)
	if err != nil {
		return nil, err
	}
	if err = validateBusinessCollectionProductIDs(productIDs, false); err != nil {
		return nil, err
	}
	if err = validateBusinessID("catalog session", catalogSessionID); err != nil {
		return nil, err
	}
	return map[string]any{"input": map[string]any{"collection": map[string]any{
		"name": name, "product_ids": productIDs, "biz_jid": jid.ToNonAD().String(), "catalog_session_id": catalogSessionID,
	}}}, nil
}

func buildUpdateBusinessCollectionVariables(jid types.JID, collectionID string, update types.BusinessCollectionUpdate, catalogSessionID string) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if err := validateBusinessID("collection", collectionID); err != nil {
		return nil, err
	}
	if err := validateBusinessID("catalog session", catalogSessionID); err != nil {
		return nil, err
	}
	if err := validateBusinessCollectionProductIDs(update.AddProductIDs, true); err != nil {
		return nil, err
	}
	if err := validateBusinessCollectionProductIDs(update.RemoveProductIDs, true); err != nil {
		return nil, err
	}
	if update.Name == nil && len(update.AddProductIDs) == 0 && len(update.RemoveProductIDs) == 0 {
		return nil, fmt.Errorf("business collection update is empty")
	}
	removed := make(map[string]struct{}, len(update.RemoveProductIDs))
	for _, productID := range update.RemoveProductIDs {
		removed[productID] = struct{}{}
	}
	for _, productID := range update.AddProductIDs {
		if _, exists := removed[productID]; exists {
			return nil, fmt.Errorf("product ID %q cannot be added and removed together", productID)
		}
	}
	collection := map[string]any{
		"id": collectionID, "biz_jid": jid.ToNonAD().String(), "catalog_session_id": catalogSessionID,
	}
	if update.Name != nil {
		name, err := validateBusinessCollectionName(*update.Name)
		if err != nil {
			return nil, err
		}
		collection["name"] = name
	}
	if len(update.AddProductIDs) > 0 {
		collection["add"] = map[string]any{"ids": update.AddProductIDs}
	}
	if len(update.RemoveProductIDs) > 0 {
		collection["remove"] = map[string]any{"ids": update.RemoveProductIDs}
	}
	return map[string]any{"input": map[string]any{"collection": collection}}, nil
}

func buildDeleteBusinessCollectionsVariables(jid types.JID, collectionIDs []string, catalogSessionID string) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if len(collectionIDs) < 1 || len(collectionIDs) > maxBusinessCollectionItems {
		return nil, fmt.Errorf("business collection delete must contain between 1 and %d IDs", maxBusinessCollectionItems)
	}
	if err := validateBusinessID("catalog session", catalogSessionID); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(collectionIDs))
	for _, collectionID := range collectionIDs {
		if err := validateBusinessID("collection", collectionID); err != nil {
			return nil, err
		}
		if _, exists := seen[collectionID]; exists {
			return nil, fmt.Errorf("duplicate collection ID %q", collectionID)
		}
		seen[collectionID] = struct{}{}
	}
	return map[string]any{"input": map[string]any{"collections": map[string]any{
		"collection_ids": collectionIDs, "biz_jid": jid.ToNonAD().String(), "catalog_session_id": catalogSessionID,
	}}}, nil
}

func buildReorderBusinessCollectionsVariables(jid types.JID, moves []types.BusinessCollectionMove) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if len(moves) < 1 || len(moves) > maxBusinessCollectionMoves {
		return nil, fmt.Errorf("business collection reorder must contain between 1 and %d moves", maxBusinessCollectionMoves)
	}
	items := make([]map[string]any, len(moves))
	seen := make(map[string]struct{}, len(moves))
	for index, move := range moves {
		if err := validateBusinessID("collection", move.CollectionID); err != nil {
			return nil, err
		}
		if move.FromIndex < 0 || move.ToIndex < 0 || move.FromIndex >= maxBusinessCollectionMoves || move.ToIndex >= maxBusinessCollectionMoves {
			return nil, fmt.Errorf("business collection move index must be between 0 and %d", maxBusinessCollectionMoves-1)
		}
		if _, exists := seen[move.CollectionID]; exists {
			return nil, fmt.Errorf("duplicate collection move %q", move.CollectionID)
		}
		seen[move.CollectionID] = struct{}{}
		items[index] = map[string]any{"collection_id": move.CollectionID, "from_index": move.FromIndex, "to_index": move.ToIndex}
	}
	return map[string]any{"input": map[string]any{"biz_jid": jid.ToNonAD().String(), "move": items}}, nil
}

func decodeBusinessCollectionMutation(data json.RawMessage, discriminator string) (*types.BusinessCollectionMutationResult, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode business collection mutation response: %w", err)
	}
	raw, ok := envelope[discriminator]
	if !ok {
		return nil, fmt.Errorf("business collection mutation response is missing %s", discriminator)
	}
	var response struct {
		Collection *struct {
			ID     string `json:"id"`
			Status *struct {
				Status string `json:"status"`
			} `json:"status_info"`
		} `json:"collection"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", discriminator, err)
	}
	if response.Collection == nil || response.Collection.ID == "" || response.Collection.Status == nil || response.Collection.Status.Status == "" {
		return nil, fmt.Errorf("%s response is missing collection status", discriminator)
	}
	return &types.BusinessCollectionMutationResult{ID: response.Collection.ID, ReviewStatus: response.Collection.Status.Status}, nil
}

func decodeBusinessCatalogSuccess(data json.RawMessage, discriminator string) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode business catalog response: %w", err)
	}
	raw, ok := envelope[discriminator]
	if !ok {
		return fmt.Errorf("business catalog response is missing %s", discriminator)
	}
	if discriminator == "xfb_whatsapp_catalog_create" {
		var response struct {
			ProductCatalog *struct{} `json:"product_catalog"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("decode %s response: %w", discriminator, err)
		}
		if response.ProductCatalog == nil {
			return fmt.Errorf("%s response is missing product_catalog", discriminator)
		}
		return nil
	}
	var response struct {
		Success *bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode %s response: %w", discriminator, err)
	}
	if response.Success == nil || !*response.Success {
		return fmt.Errorf("%s response did not confirm success", discriminator)
	}
	return nil
}

func newBusinessCatalogSessionID() string {
	return uuid.NewString()
}

func (cli *Client) CreateBusinessCollection(ctx context.Context, name string, productIDs []string) (*types.BusinessCollectionMutationResult, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return nil, err
	}
	variables, err := buildCreateBusinessCollectionVariables(jid, name, productIDs, newBusinessCatalogSessionID())
	if err != nil {
		return nil, err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessCreateCollectionDocumentID, variables)
	if err != nil {
		return nil, fmt.Errorf("create business collection: %w", err)
	}
	return decodeBusinessCollectionMutation(data, "xfb_whatsapp_catalog_create_collection")
}

func (cli *Client) UpdateBusinessCollection(ctx context.Context, collectionID string, update types.BusinessCollectionUpdate) (*types.BusinessCollectionMutationResult, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return nil, err
	}
	variables, err := buildUpdateBusinessCollectionVariables(jid, collectionID, update, newBusinessCatalogSessionID())
	if err != nil {
		return nil, err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessUpdateCollectionDocumentID, variables)
	if err != nil {
		return nil, fmt.Errorf("update business collection: %w", err)
	}
	return decodeBusinessCollectionMutation(data, "xfb_whatsapp_catalog_update_collection")
}

func (cli *Client) DeleteBusinessCollections(ctx context.Context, collectionIDs []string) error {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return err
	}
	variables, err := buildDeleteBusinessCollectionsVariables(jid, collectionIDs, newBusinessCatalogSessionID())
	if err != nil {
		return err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessDeleteCollectionsDocumentID, variables)
	if err != nil {
		return fmt.Errorf("delete business collections: %w", err)
	}
	return decodeBusinessCatalogSuccess(data, "xfb_whatsapp_catalog_delete_collections")
}

func (cli *Client) ReorderBusinessCollections(ctx context.Context, moves []types.BusinessCollectionMove) error {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return err
	}
	variables, err := buildReorderBusinessCollectionsVariables(jid, moves)
	if err != nil {
		return err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessReorderCollectionsDocumentID, variables)
	if err != nil {
		return fmt.Errorf("reorder business collections: %w", err)
	}
	return decodeBusinessCatalogSuccess(data, "xfb_whatsapp_catalog_update_collection_list")
}

const (
	businessCreateCatalogDocumentID     = "29232780583035464"
	businessUpdateCommerceDocumentID    = "9797519763673469"
	businessProductVisibilityDocumentID = "9665162096898581"
	businessAppealProductDocumentID     = "29276343172013990"
	businessAppealCollectionDocumentID  = "9971242039605207"
	maxBusinessCatalogAppealReasonBytes = 4096
)

func buildCreateBusinessCatalogVariables(jid types.JID) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	return map[string]any{"input": map[string]any{
		"product_catalog": map[string]any{"biz_jid": jid.ToNonAD().String()},
		"platform":        "WEB",
	}}, nil
}

func buildBusinessCartVariables(jid types.JID, enabled bool) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	return map[string]any{"input": map[string]any{
		"biz_jid": jid.ToNonAD().String(), "cart_enabled": enabled,
	}}, nil
}

func buildBusinessProductVisibilityVariables(jid types.JID, productID string, hidden bool) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if err := validateBusinessID("product", productID); err != nil {
		return nil, err
	}
	return map[string]any{"input": map[string]any{
		"jid":      jid.ToNonAD().String(),
		"products": []map[string]any{{"product_id": productID, "is_hidden": hidden}},
	}}, nil
}

func validateBusinessCatalogAppealReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", fmt.Errorf("business catalog appeal reason is empty")
	}
	if len(reason) > maxBusinessCatalogAppealReasonBytes {
		return "", fmt.Errorf("business catalog appeal reason exceeds %d bytes", maxBusinessCatalogAppealReasonBytes)
	}
	return reason, nil
}

func buildBusinessProductAppealVariables(jid types.JID, productID, reason string) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if err := validateBusinessID("product", productID); err != nil {
		return nil, err
	}
	reason, err := validateBusinessCatalogAppealReason(reason)
	if err != nil {
		return nil, err
	}
	return map[string]any{"input": map[string]any{
		"jid": jid.ToNonAD().String(), "product_id": productID, "reason": reason,
	}}, nil
}

func buildBusinessCollectionAppealVariables(jid types.JID, collectionID, reason string) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if err := validateBusinessID("collection", collectionID); err != nil {
		return nil, err
	}
	reason, err := validateBusinessCatalogAppealReason(reason)
	if err != nil {
		return nil, err
	}
	return map[string]any{"input": map[string]any{
		"product_set_id": collectionID, "jid": jid.ToNonAD().String(), "reason": reason,
	}}, nil
}

func decodeBusinessCartEnabled(data json.RawMessage, expected bool) error {
	var envelope struct {
		Result *struct {
			Enabled *bool `json:"cart_enabled"`
		} `json:"xfb_whatsapp_smb_commerce_settings"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode business commerce settings response: %w", err)
	}
	if envelope.Result == nil || envelope.Result.Enabled == nil {
		return fmt.Errorf("business commerce settings response is missing cart_enabled")
	}
	if *envelope.Result.Enabled != expected {
		return fmt.Errorf("business commerce settings response returned cart_enabled=%t, expected %t", *envelope.Result.Enabled, expected)
	}
	return nil
}

func (cli *Client) CreateBusinessCatalog(ctx context.Context) error {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return err
	}
	variables, err := buildCreateBusinessCatalogVariables(jid)
	if err != nil {
		return err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessCreateCatalogDocumentID, variables)
	if err != nil {
		return fmt.Errorf("create business catalog: %w", err)
	}
	return decodeBusinessCatalogSuccess(data, "xfb_whatsapp_catalog_create")
}

func (cli *Client) SetBusinessCartEnabled(ctx context.Context, enabled bool) error {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return err
	}
	variables, err := buildBusinessCartVariables(jid, enabled)
	if err != nil {
		return err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessUpdateCommerceDocumentID, variables)
	if err != nil {
		return fmt.Errorf("update business cart setting: %w", err)
	}
	return decodeBusinessCartEnabled(data, enabled)
}

func (cli *Client) SetBusinessProductVisibility(ctx context.Context, productID string, hidden bool) error {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return err
	}
	variables, err := buildBusinessProductVisibilityVariables(jid, productID, hidden)
	if err != nil {
		return err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessProductVisibilityDocumentID, variables)
	if err != nil {
		return fmt.Errorf("update business product visibility: %w", err)
	}
	return decodeBusinessCatalogSuccess(data, "xfb_whatsapp_catalog_product_visibility_update")
}

func (cli *Client) AppealBusinessProduct(ctx context.Context, productID, reason string) error {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return err
	}
	variables, err := buildBusinessProductAppealVariables(jid, productID, reason)
	if err != nil {
		return err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessAppealProductDocumentID, variables)
	if err != nil {
		return fmt.Errorf("appeal business product: %w", err)
	}
	return decodeBusinessCatalogSuccess(data, "xfb_whatsapp_catalog_appeal_product")
}

func (cli *Client) AppealBusinessCollection(ctx context.Context, collectionID, reason string) error {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return err
	}
	variables, err := buildBusinessCollectionAppealVariables(jid, collectionID, reason)
	if err != nil {
		return err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessAppealCollectionDocumentID, variables)
	if err != nil {
		return fmt.Errorf("appeal business collection: %w", err)
	}
	return decodeBusinessCatalogSuccess(data, "xfb_whatsapp_catalog_appeal_collection")
}

const (
	businessCatalogGraphQLEndpoint          = "https://graph.whatsapp.com/graphql/catalog"
	businessCatalogGraphQLAccessToken       = "WA|787118555984857|7bb1544a3599aa180ac9a3f7688ba243"
	businessGetMerchantComplianceDocumentID = "25960403573553316"
	businessSetMerchantComplianceDocumentID = "25188352884120072"
	maxBusinessMerchantNameBytes            = 256
	maxBusinessMerchantEmailBytes           = 254
	maxBusinessMerchantPhoneBytes           = 64
)

func validateBusinessMerchantEntityType(entityType types.BusinessMerchantEntityType) error {
	switch entityType {
	case types.BusinessMerchantEntitySoleProprietorship,
		types.BusinessMerchantEntityPartnership,
		types.BusinessMerchantEntityPrivateCompany,
		types.BusinessMerchantEntityPublicCompany,
		types.BusinessMerchantEntityLimitedLiabilityPartnership,
		types.BusinessMerchantEntityOther:
		return nil
	default:
		return fmt.Errorf("unsupported business merchant entity type %q", entityType)
	}
}

func validateBusinessMerchantField(name, value string, limit int) error {
	if len(value) > limit {
		return fmt.Errorf("business merchant %s exceeds %d bytes", name, limit)
	}
	return nil
}

func normalizeBusinessMerchantCompliance(info types.BusinessMerchantCompliance) (types.BusinessMerchantCompliance, error) {
	info.EntityName = strings.TrimSpace(info.EntityName)
	info.EntityTypeCustom = strings.TrimSpace(info.EntityTypeCustom)
	info.CustomerCare.Email = strings.TrimSpace(info.CustomerCare.Email)
	info.CustomerCare.LandlineNumber = strings.TrimSpace(info.CustomerCare.LandlineNumber)
	info.CustomerCare.MobileNumber = strings.TrimSpace(info.CustomerCare.MobileNumber)
	info.GrievanceOfficer.Name = strings.TrimSpace(info.GrievanceOfficer.Name)
	info.GrievanceOfficer.Email = strings.TrimSpace(info.GrievanceOfficer.Email)
	info.GrievanceOfficer.LandlineNumber = strings.TrimSpace(info.GrievanceOfficer.LandlineNumber)
	info.GrievanceOfficer.MobileNumber = strings.TrimSpace(info.GrievanceOfficer.MobileNumber)
	if info.EntityName == "" {
		return info, fmt.Errorf("business merchant entity name is empty")
	}
	if info.EntityType == "" {
		return info, fmt.Errorf("business merchant entity type is empty")
	}
	if err := validateBusinessMerchantEntityType(info.EntityType); err != nil {
		return info, err
	}
	if info.EntityType == types.BusinessMerchantEntityOther && info.EntityTypeCustom == "" {
		return info, fmt.Errorf("business merchant custom entity type is empty")
	}
	fields := []struct {
		name  string
		value string
		limit int
	}{
		{"entity name", info.EntityName, maxBusinessMerchantNameBytes},
		{"custom entity type", info.EntityTypeCustom, maxBusinessMerchantNameBytes},
		{"customer care email", info.CustomerCare.Email, maxBusinessMerchantEmailBytes},
		{"customer care landline", info.CustomerCare.LandlineNumber, maxBusinessMerchantPhoneBytes},
		{"customer care mobile", info.CustomerCare.MobileNumber, maxBusinessMerchantPhoneBytes},
		{"grievance officer name", info.GrievanceOfficer.Name, maxBusinessMerchantNameBytes},
		{"grievance officer email", info.GrievanceOfficer.Email, maxBusinessMerchantEmailBytes},
		{"grievance officer landline", info.GrievanceOfficer.LandlineNumber, maxBusinessMerchantPhoneBytes},
		{"grievance officer mobile", info.GrievanceOfficer.MobileNumber, maxBusinessMerchantPhoneBytes},
	}
	for _, field := range fields {
		if err := validateBusinessMerchantField(field.name, field.value, field.limit); err != nil {
			return info, err
		}
	}
	return info, nil
}

func buildBusinessMerchantComplianceQueryVariables(jid types.JID) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	return map[string]any{"request": map[string]any{"biz_jid": jid.ToNonAD().String()}}, nil
}

func buildBusinessMerchantComplianceVariables(jid types.JID, info types.BusinessMerchantCompliance) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	info, err := normalizeBusinessMerchantCompliance(info)
	if err != nil {
		return nil, err
	}
	return map[string]any{"input": map[string]any{
		"biz_jid": jid.ToNonAD().String(),
		"merchant_info": map[string]any{
			"entity_name": info.EntityName, "entity_type": string(info.EntityType),
			"is_registered": info.IsRegistered, "entity_type_custom": info.EntityTypeCustom,
			"customer_care_details": map[string]any{
				"email": info.CustomerCare.Email, "landline_number": info.CustomerCare.LandlineNumber, "mobile_number": info.CustomerCare.MobileNumber,
			},
			"grievance_officer_details": map[string]any{
				"name": info.GrievanceOfficer.Name, "email": info.GrievanceOfficer.Email,
				"landline_number": info.GrievanceOfficer.LandlineNumber, "mobile_number": info.GrievanceOfficer.MobileNumber,
			},
		},
	}}, nil
}

func decodeBusinessMerchantCompliance(data json.RawMessage, field string) (*types.BusinessMerchantCompliance, error) {
	var envelope map[string]struct {
		MerchantInfo *types.BusinessMerchantCompliance `json:"merchant_info"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode business merchant compliance response: %w", err)
	}
	result, ok := envelope[field]
	if !ok || result.MerchantInfo == nil {
		return nil, fmt.Errorf("business merchant compliance response is missing merchant_info")
	}
	return result.MerchantInfo, nil
}

func (cli *Client) GetBusinessMerchantCompliance(ctx context.Context) (*types.BusinessMerchantCompliance, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return nil, err
	}
	variables, err := buildBusinessMerchantComplianceQueryVariables(jid)
	if err != nil {
		return nil, err
	}
	data, err := cli.sendBusinessFacebookGraphQL(ctx, businessCatalogGraphQLEndpoint, businessGetMerchantComplianceDocumentID, businessCatalogGraphQLAccessToken, variables)
	if err != nil {
		return nil, fmt.Errorf("get business merchant compliance: %w", err)
	}
	return decodeBusinessMerchantCompliance(data, "xfb_whatsapp_biz_merchant_compliance_info")
}

func (cli *Client) SetBusinessMerchantCompliance(ctx context.Context, info types.BusinessMerchantCompliance) (*types.BusinessMerchantCompliance, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return nil, err
	}
	variables, err := buildBusinessMerchantComplianceVariables(jid, info)
	if err != nil {
		return nil, err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessSetMerchantComplianceDocumentID, variables)
	if err != nil {
		return nil, fmt.Errorf("set business merchant compliance: %w", err)
	}
	return decodeBusinessMerchantCompliance(data, "xfb_whatsapp_biz_merchant_set_compliance_info")
}

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
	if strings.TrimSpace(params.Body) == "" || !bounded(params.Body, 1024) || strings.TrimSpace(params.ButtonText) == "" || !utf8.ValidString(params.ButtonText) || !bounded(params.ButtonText, 20) || !bounded(params.Footer, 60) {
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
	if strings.TrimSpace(params.Body) == "" || !bounded(params.Body, 1024) || strings.TrimSpace(params.ButtonText) == "" || !utf8.ValidString(params.ButtonText) || !bounded(params.ButtonText, 20) || !bounded(params.Footer, 60) {
		return nil, errors.New("invalid business flow message text")
	}
	if strings.TrimSpace(params.FlowID) == "" || !utf8.ValidString(params.FlowID) || !bounded(params.FlowID, 256) || strings.TrimSpace(params.FlowToken) == "" || !utf8.ValidString(params.FlowToken) || !bounded(params.FlowToken, 8192) {
		return nil, errors.New("invalid business flow identity")
	}
	if params.FlowAction != "navigate" && params.FlowAction != "data_exchange" {
		return nil, errors.New("invalid business flow action")
	}
	if !utf8.ValidString(params.Screen) || !bounded(params.Screen, 256) || (params.FlowAction == "navigate" && strings.TrimSpace(params.Screen) == "") {
		return nil, errors.New("invalid business flow screen")
	}
	if params.FlowAction == "data_exchange" && (params.Screen != "" || params.DataJSON != "") {
		return nil, errors.New("data-exchange flow messages cannot include an action payload")
	}
	if !utf8.ValidString(params.DataJSON) || !bounded(params.DataJSON, 16*1024) {
		return nil, errors.New("business flow data is too large")
	}
	var data *map[string]json.RawMessage
	if params.DataJSON != "" {
		parsed := make(map[string]json.RawMessage)
		if err := json.Unmarshal([]byte(params.DataJSON), &parsed); err != nil || parsed == nil {
			return nil, errors.New("business flow data must be a JSON object")
		}
		data = &parsed
	}
	type actionPayload struct {
		Screen string                      `json:"screen,omitempty"`
		Data   *map[string]json.RawMessage `json:"data,omitempty"`
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

const (
	businessGraphQLEndpoint         = "https://graph.facebook.com/graphql"
	businessAddProductDocumentID    = "24249359867999500"
	businessEditProductDocumentID   = "9889773371084956"
	businessDeleteProductDocumentID = "9376108569185474"
	businessTokenRequestTimeout     = 30 * time.Second
	maxBusinessGraphQLResponseBytes = 4 * 1024 * 1024
	maxBusinessProductImageBytes    = 16 * 1024 * 1024
)

var (
	ErrBusinessTokenRecoveryRequired = errors.New("business access token recovery is required on the primary device")
	ErrBusinessTokenTooManyAttempts  = errors.New("business access token request was rate limited")
	errBusinessIncorrectNonce        = errors.New("business access token nonce was rejected")
)

const businessNonceDeliveredAttr = "__whatsmeow_business_nonce_delivered"

type businessAccessToken struct {
	accessToken string
	actorID     string
}

type businessNonceWaiter struct {
	ch chan string
}

type businessCatalogAuthState struct {
	tokenLock   chan struct{}
	token       businessAccessToken
	nonceWaiter atomic.Pointer[businessNonceWaiter]
}

type businessGraphQLErrorItem struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type businessGraphQLError struct {
	StatusCode int
	Errors     []businessGraphQLErrorItem
}

func (err *businessGraphQLError) Error() string {
	if len(err.Errors) == 0 {
		return fmt.Sprintf("business GraphQL request failed with status %d", err.StatusCode)
	}
	codes := make([]string, 0, len(err.Errors))
	for _, item := range err.Errors {
		codes = append(codes, strconv.Itoa(item.Code))
	}
	return "business GraphQL request failed with error code(s) " + strings.Join(codes, ",")
}

func isBusinessGraphQLAuthError(err error) bool {
	var graphErr *businessGraphQLError
	if !errors.As(err, &graphErr) {
		return false
	}
	if graphErr.StatusCode == http.StatusUnauthorized || graphErr.StatusCode == http.StatusForbidden {
		return true
	}
	for _, item := range graphErr.Errors {
		if item.Code == 190 || item.Code == 400 {
			return true
		}
	}
	return false
}

func validateBusinessProductInput(input types.BusinessProductInput) error {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return fmt.Errorf("business product name is empty")
	}
	if len(input.Name) > 256 {
		return fmt.Errorf("business product name exceeds 256 bytes")
	}
	if len(input.Description) > 4096 {
		return fmt.Errorf("business product description exceeds 4096 bytes")
	}
	if len(input.RetailerID) > 256 {
		return fmt.Errorf("business product retailer ID exceeds 256 bytes")
	}
	if len(input.ImageURLs) < 1 || len(input.ImageURLs) > 10 {
		return fmt.Errorf("business product must contain between 1 and 10 images")
	}
	for _, rawURL := range input.ImageURLs {
		if err := validateBusinessMediaURL(rawURL); err != nil {
			return fmt.Errorf("invalid business product image URL: %w", err)
		}
	}
	if len(input.VideoURLs) > 10 {
		return fmt.Errorf("business product cannot contain more than 10 videos")
	}
	for _, rawURL := range input.VideoURLs {
		if err := validateBusinessMediaURL(rawURL); err != nil {
			return fmt.Errorf("invalid business product video URL: %w", err)
		}
	}
	if input.URL != "" {
		parsed, err := url.ParseRequestURI(input.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || len(input.URL) > 2048 {
			return fmt.Errorf("business product URL must be an absolute HTTPS URL of at most 2048 bytes")
		}
	}
	if input.Price == "" {
		if input.Currency != "" || input.SalePrice != "" {
			return fmt.Errorf("business product currency and sale price require a price")
		}
	} else {
		if !isUppercaseCurrency(input.Currency) {
			return fmt.Errorf("business product currency must be a three-letter uppercase code")
		}
		if !isUnsignedDecimal(input.Price) {
			return fmt.Errorf("business product price must be an integer amount in thousandths")
		}
		if input.SalePrice != "" && !isUnsignedDecimal(input.SalePrice) {
			return fmt.Errorf("business product sale price must be an integer amount in thousandths")
		}
	}
	if input.ComplianceCategory != "" && len(input.ComplianceCategory) > 128 {
		return fmt.Errorf("business product compliance category exceeds 128 bytes")
	}
	if input.Compliance != nil {
		if len(input.Compliance.CountryCodeOrigin) > 3 || len(input.Compliance.ImporterName) > 256 {
			return fmt.Errorf("business product compliance information is invalid")
		}
		if address := input.Compliance.ImporterAddress; address != nil {
			if len(address.Street1) > 512 || len(address.Street2) > 512 || len(address.City) > 256 || len(address.Region) > 256 || len(address.PostalCode) > 64 || len(address.CountryCode) > 3 {
				return fmt.Errorf("business product importer address is invalid")
			}
		}
	}
	return nil
}

func isUnsignedDecimal(value string) bool {
	if value == "" || len(value) > 18 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isUppercaseCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func validateBusinessMediaURL(rawURL string) error {
	if len(rawURL) > 4096 {
		return fmt.Errorf("URL exceeds 4096 bytes")
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("URL must be absolute HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "whatsapp.net" && !strings.HasSuffix(host, ".whatsapp.net") && host != "fbcdn.net" && !strings.HasSuffix(host, ".fbcdn.net") && host != "facebook.com" && !strings.HasSuffix(host, ".facebook.com") {
		return fmt.Errorf("URL must use a WhatsApp or Meta media host")
	}
	return nil
}

func buildBusinessProductInfo(input types.BusinessProductInput) map[string]any {
	images := make([]map[string]any, len(input.ImageURLs))
	for index, imageURL := range input.ImageURLs {
		images[index] = map[string]any{"url": imageURL}
	}
	media := map[string]any{"image": images}
	if len(input.VideoURLs) > 0 {
		videos := make([]map[string]any, len(input.VideoURLs))
		for index, videoURL := range input.VideoURLs {
			videos[index] = map[string]any{"url": videoURL}
		}
		media["video"] = videos
	}
	info := map[string]any{
		"name":      strings.TrimSpace(input.Name),
		"media":     media,
		"is_hidden": input.Hidden,
	}
	if input.Description != "" {
		info["description"] = input.Description
	}
	if input.URL != "" {
		info["url"] = input.URL
	}
	if input.RetailerID != "" {
		info["retailer_id"] = input.RetailerID
	}
	if input.Price != "" {
		info["currency"] = input.Currency
		info["price"] = input.Price
	}
	if input.SalePrice != "" {
		info["sale_price"] = input.SalePrice
	}
	if input.Compliance != nil {
		compliance := map[string]any{"country_code_origin": input.Compliance.CountryCodeOrigin}
		if input.Compliance.ImporterName != "" {
			compliance["importer_name"] = input.Compliance.ImporterName
		}
		if address := input.Compliance.ImporterAddress; address != nil {
			addressInput := map[string]any{
				"country_code": address.CountryCode,
				"city":         address.City,
				"street1":      address.Street1,
			}
			if address.Street2 != "" {
				addressInput["street2"] = address.Street2
			}
			if address.Region != "" {
				addressInput["region"] = address.Region
			}
			if address.PostalCode != "" {
				addressInput["postal_code"] = address.PostalCode
			}
			compliance["importer_address"] = addressInput
		}
		info["compliance_info"] = compliance
	}
	if input.ComplianceCategory != "" {
		info["compliance_category"] = input.ComplianceCategory
	}
	return info
}

func buildBusinessProductMutationVariables(jid types.JID, productID string, input types.BusinessProductInput, width, height int) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if productID != "" {
		if err := validateBusinessID("product", productID); err != nil {
			return nil, err
		}
	}
	if err := validateBusinessProductInput(input); err != nil {
		return nil, err
	}
	width, height, err := normalizeDimensions(width, height)
	if err != nil {
		return nil, err
	}
	product := map[string]any{
		"biz_jid":      jid.ToNonAD().String(),
		"width":        width,
		"height":       height,
		"product_info": buildBusinessProductInfo(input),
	}
	if productID != "" {
		product["product_id"] = productID
	}
	return map[string]any{"input": map[string]any{"product": product}}, nil
}

func buildDeleteBusinessProductsVariables(jid types.JID, productIDs []string) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if len(productIDs) < 1 || len(productIDs) > 100 {
		return nil, fmt.Errorf("business product delete must contain between 1 and 100 IDs")
	}
	seen := make(map[string]struct{}, len(productIDs))
	for _, productID := range productIDs {
		if err := validateBusinessID("product", productID); err != nil {
			return nil, err
		}
		if _, exists := seen[productID]; exists {
			return nil, fmt.Errorf("duplicate product ID %q", productID)
		}
		seen[productID] = struct{}{}
	}
	return map[string]any{"input": map[string]any{
		"biz_jid":     jid.ToNonAD().String(),
		"product_ids": productIDs,
	}}, nil
}

func decodeBusinessProductMutation(data json.RawMessage, discriminator string) (*types.BusinessProduct, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode business product mutation response: %w", err)
	}
	raw, ok := envelope[discriminator]
	if !ok {
		return nil, fmt.Errorf("business product mutation response is missing %s", discriminator)
	}
	var result struct {
		Product *types.BusinessProduct `json:"product"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", discriminator, err)
	}
	if result.Product == nil || result.Product.ID == "" {
		return nil, fmt.Errorf("%s response is missing product", discriminator)
	}
	return result.Product, nil
}

func decodeDeleteBusinessProducts(data json.RawMessage) (int, error) {
	var envelope struct {
		Result *struct {
			DeletedCount *int `json:"deleted_count"`
		} `json:"xfb_whatsapp_catalog_delete_product"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return 0, fmt.Errorf("decode business product delete response: %w", err)
	}
	if envelope.Result == nil || envelope.Result.DeletedCount == nil || *envelope.Result.DeletedCount < 0 {
		return 0, fmt.Errorf("business product delete response is missing deleted_count")
	}
	return *envelope.Result.DeletedCount, nil
}

func businessSilentNonceQuery() infoQuery {
	return infoQuery{Namespace: "fb:thrift_iq", Type: iqGet, To: types.ServerJID, SMaxID: "118", NoRetry: true, Timeout: businessTokenRequestTimeout}
}

func businessTokenExchangeQuery(nonce string) (infoQuery, error) {
	if strings.TrimSpace(nonce) == "" || len(nonce) > 8192 {
		return infoQuery{}, fmt.Errorf("business access token nonce is invalid")
	}
	return infoQuery{
		Namespace: "fb:thrift_iq",
		Type:      iqGet,
		To:        types.ServerJID,
		SMaxID:    "104",
		NoRetry:   true,
		Timeout:   businessTokenRequestTimeout,
		Content:   []waBinary.Node{{Tag: "parameters", Content: []waBinary.Node{{Tag: "code", Content: []byte(nonce)}}}},
	}, nil
}

func parseBusinessTokenResponse(node *waBinary.Node) (businessAccessToken, error) {
	if node == nil {
		return businessAccessToken{}, fmt.Errorf("business access token response is empty")
	}
	accessTokenNode, ok := node.GetOptionalChildByTag("access_token")
	if !ok {
		return businessAccessToken{}, fmt.Errorf("business access token response is missing access_token")
	}
	personNode, ok := node.GetOptionalChildByTag("business_person")
	if !ok {
		return businessAccessToken{}, fmt.Errorf("business access token response is missing business_person")
	}
	accessToken, ok := accessTokenNode.Content.([]byte)
	if !ok || len(accessToken) == 0 || len(accessToken) > 16384 {
		return businessAccessToken{}, fmt.Errorf("business access token response contains an invalid token")
	}
	actorID := personNode.AttrGetter().String("id")
	if actorID == "" || len(actorID) > 256 {
		return businessAccessToken{}, fmt.Errorf("business access token response contains an invalid business person")
	}
	return businessAccessToken{accessToken: string(accessToken), actorID: actorID}, nil
}

func (cli *Client) getBusinessCatalogAuth() *businessCatalogAuthState {
	if existing := cli.businessCatalogAuth.Load(); existing != nil {
		return existing
	}
	created := &businessCatalogAuthState{tokenLock: make(chan struct{}, 1)}
	created.tokenLock <- struct{}{}
	if cli.businessCatalogAuth.CompareAndSwap(nil, created) {
		return created
	}
	return cli.businessCatalogAuth.Load()
}

func (cli *Client) handleBusinessCatalogNotification(node *waBinary.Node) {
	state := cli.businessCatalogAuth.Load()
	if state == nil {
		return
	}
	nonceNode, ok := node.GetOptionalChildByTag("wa_ad_account_nonce")
	if !ok {
		return
	}
	nonce, ok := nonceNode.Content.([]byte)
	if !ok || len(nonce) == 0 || len(nonce) > 8192 {
		return
	}
	waiter := state.nonceWaiter.Load()
	if waiter == nil {
		return
	}
	select {
	case waiter.ch <- string(nonce):
	default:
	}
}

func (cli *Client) handleQueuedBusinessCatalogNotification(node *waBinary.Node) {
	if delivered, _ := node.Attrs[businessNonceDeliveredAttr].(bool); !delivered {
		cli.handleBusinessCatalogNotification(node)
	}
}

func parseBusinessNonceRequestResponse(node *waBinary.Node) error {
	result, ok := node.GetOptionalChildByTag("result")
	if !ok {
		return fmt.Errorf("business nonce response is missing result")
	}
	switch result.AttrGetter().String("status") {
	case "Success":
		return nil
	case "RecoveryRequired":
		return ErrBusinessTokenRecoveryRequired
	default:
		return fmt.Errorf("business nonce request returned an unknown status")
	}
}

func classifyBusinessTokenExchangeError(node *waBinary.Node, err error) error {
	if node != nil {
		if errorNode, ok := node.GetOptionalChildByTag("error"); ok {
			switch errorNode.AttrGetter().String("code") {
			case "432":
				return errBusinessIncorrectNonce
			case "431":
				return ErrBusinessTokenTooManyAttempts
			}
		}
	}
	return err
}

func (cli *Client) acquireBusinessAccessToken(ctx context.Context, state *businessCatalogAuthState) (businessAccessToken, error) {
	waitCtx, cancel := context.WithTimeout(ctx, businessTokenRequestTimeout)
	defer cancel()
	waiter := &businessNonceWaiter{ch: make(chan string, 1)}
	state.nonceWaiter.Store(waiter)
	defer state.nonceWaiter.CompareAndSwap(waiter, nil)

	response, err := cli.sendIQ(waitCtx, businessSilentNonceQuery())
	if err != nil {
		return businessAccessToken{}, fmt.Errorf("request business access token nonce: %w", err)
	}
	if err = parseBusinessNonceRequestResponse(response); err != nil {
		return businessAccessToken{}, err
	}

	var nonce string
	select {
	case nonce = <-waiter.ch:
	case <-waitCtx.Done():
		return businessAccessToken{}, fmt.Errorf("wait for business access token nonce: %w", waitCtx.Err())
	}
	exchange, err := businessTokenExchangeQuery(nonce)
	if err != nil {
		return businessAccessToken{}, err
	}
	response, err = cli.sendIQ(waitCtx, exchange)
	if err != nil {
		return businessAccessToken{}, classifyBusinessTokenExchangeError(response, err)
	}
	return parseBusinessTokenResponse(response)
}

func (cli *Client) businessAccessToken(ctx context.Context) (businessAccessToken, error) {
	state := cli.getBusinessCatalogAuth()
	select {
	case <-state.tokenLock:
		defer func() { state.tokenLock <- struct{}{} }()
	case <-ctx.Done():
		return businessAccessToken{}, ctx.Err()
	}
	if state.token.accessToken != "" {
		return state.token, nil
	}
	var token businessAccessToken
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		token, err = cli.acquireBusinessAccessToken(ctx, state)
		if !errors.Is(err, errBusinessIncorrectNonce) {
			break
		}
	}
	if err != nil {
		return businessAccessToken{}, err
	}
	state.token = token
	return token, nil
}

func (cli *Client) invalidateBusinessAccessToken(ctx context.Context, token string) error {
	state := cli.businessCatalogAuth.Load()
	if state == nil {
		return nil
	}
	select {
	case <-state.tokenLock:
	case <-ctx.Done():
		return ctx.Err()
	}
	if state.token.accessToken == token {
		state.token = businessAccessToken{}
	}
	state.tokenLock <- struct{}{}
	return nil
}

func (cli *Client) sendBusinessFacebookGraphQL(ctx context.Context, endpoint, documentID, accessToken string, variables map[string]any) (json.RawMessage, error) {
	if cli == nil {
		return nil, ErrClientIsNil
	}
	if cli.mediaHTTP == nil {
		return nil, fmt.Errorf("business GraphQL HTTP client is not configured")
	}
	body := struct {
		AccessToken string         `json:"access_token"`
		DocumentID  string         `json:"doc_id"`
		Variables   map[string]any `json:"variables"`
		Locale      string         `json:"locale"`
	}{AccessToken: accessToken, DocumentID: documentID, Variables: variables, Locale: "en_US"}
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(body); err != nil {
		return nil, fmt.Errorf("encode business GraphQL request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &encoded)
	if err != nil {
		return nil, fmt.Errorf("prepare business GraphQL request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", socket.Origin)
	request.Header.Set("Referer", socket.Origin+"/")
	if jid := cli.Store.GetJID(); !jid.IsEmpty() && jid.Device > 0 {
		request.Header.Set("X-WA-Device-ID", strconv.FormatUint(uint64(jid.Device), 10))
	}
	response, err := cli.mediaHTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute business GraphQL request: %w", err)
	}
	defer drainAndClose(response.Body)
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBusinessGraphQLResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read business GraphQL response: %w", err)
	}
	if len(raw) > maxBusinessGraphQLResponseBytes {
		return nil, fmt.Errorf("business GraphQL response exceeds %d bytes", maxBusinessGraphQLResponseBytes)
	}
	var envelope struct {
		Data   json.RawMessage            `json:"data"`
		Errors []businessGraphQLErrorItem `json:"errors"`
		Error  *businessGraphQLErrorItem  `json:"error"`
	}
	err = json.Unmarshal(raw, &envelope)
	if err == nil && envelope.Error != nil {
		envelope.Errors = append(envelope.Errors, *envelope.Error)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &businessGraphQLError{StatusCode: response.StatusCode, Errors: envelope.Errors}
	}
	if err != nil {
		return nil, fmt.Errorf("decode business GraphQL response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return nil, &businessGraphQLError{StatusCode: response.StatusCode, Errors: envelope.Errors}
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return nil, fmt.Errorf("business GraphQL response is missing data")
	}
	return envelope.Data, nil
}

func businessCatalogMutationVariablesWithActor(variables map[string]any, actorID string) (map[string]any, error) {
	if strings.TrimSpace(actorID) == "" {
		return nil, fmt.Errorf("business catalog mutation actor ID is empty")
	}
	input, ok := variables["input"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("business catalog mutation variables are missing input")
	}
	result := make(map[string]any, len(variables))
	for key, value := range variables {
		result[key] = value
	}
	actorInput := make(map[string]any, len(input)+1)
	for key, value := range input {
		actorInput[key] = value
	}
	actorInput["actor_id"] = actorID
	result["input"] = actorInput
	return result, nil
}

func (cli *Client) executeBusinessCatalogMutation(ctx context.Context, documentID string, variables map[string]any) (json.RawMessage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := cli.businessAccessToken(ctx)
		if err != nil {
			return nil, err
		}
		requestVariables, err := businessCatalogMutationVariablesWithActor(variables, token.actorID)
		if err != nil {
			return nil, err
		}
		data, err := cli.sendBusinessFacebookGraphQL(ctx, businessGraphQLEndpoint, documentID, token.accessToken, requestVariables)
		if err == nil {
			return data, nil
		}
		if attempt == 0 && isBusinessGraphQLAuthError(err) {
			if err = cli.invalidateBusinessAccessToken(ctx, token.accessToken); err != nil {
				return nil, err
			}
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("business catalog mutation failed after token refresh")
}

func (cli *Client) ownBusinessJID() (types.JID, error) {
	if cli == nil {
		return types.EmptyJID, ErrClientIsNil
	}
	jid := cli.Store.GetJID().ToNonAD()
	if err := validateBusinessJID(jid); err != nil {
		return types.EmptyJID, fmt.Errorf("business product mutation requires a paired client: %w", err)
	}
	return jid, nil
}

func (cli *Client) CreateBusinessProduct(ctx context.Context, input types.BusinessProductInput, width, height int) (*types.BusinessProduct, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return nil, err
	}
	variables, err := buildBusinessProductMutationVariables(jid, "", input, width, height)
	if err != nil {
		return nil, err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessAddProductDocumentID, variables)
	if err != nil {
		return nil, fmt.Errorf("create business product: %w", err)
	}
	return decodeBusinessProductMutation(data, "xfb_whatsapp_catalog_add_product")
}

func (cli *Client) UpdateBusinessProduct(ctx context.Context, productID string, input types.BusinessProductInput, width, height int) (*types.BusinessProduct, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return nil, err
	}
	variables, err := buildBusinessProductMutationVariables(jid, productID, input, width, height)
	if err != nil {
		return nil, err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessEditProductDocumentID, variables)
	if err != nil {
		return nil, fmt.Errorf("update business product: %w", err)
	}
	return decodeBusinessProductMutation(data, "xfb_whatsapp_catalog_edit_product")
}

func (cli *Client) DeleteBusinessProducts(ctx context.Context, productIDs []string) (int, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return 0, err
	}
	variables, err := buildDeleteBusinessProductsVariables(jid, productIDs)
	if err != nil {
		return 0, err
	}
	data, err := cli.executeBusinessCatalogMutation(ctx, businessDeleteProductDocumentID, variables)
	if err != nil {
		return 0, fmt.Errorf("delete business products: %w", err)
	}
	return decodeDeleteBusinessProducts(data)
}

func validateBusinessProductImage(image []byte) ([]byte, error) {
	if len(image) == 0 {
		return nil, fmt.Errorf("business product image is empty")
	}
	if len(image) > maxBusinessProductImageBytes {
		return nil, fmt.Errorf("business product image exceeds %d bytes", maxBusinessProductImageBytes)
	}
	mimeType := http.DetectContentType(image)
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		return nil, fmt.Errorf("business product image must be JPEG or PNG")
	}
	hash := sha256.Sum256(image)
	return hash[:], nil
}

func (cli *Client) UploadBusinessProductImage(ctx context.Context, image []byte) (string, error) {
	hash, err := validateBusinessProductImage(image)
	if err != nil {
		return "", err
	}
	mediaConn, err := cli.refreshMediaConn(ctx, false)
	if err != nil {
		return "", fmt.Errorf("refresh media connection for business product image: %w", err)
	}
	if len(mediaConn.Hosts) == 0 {
		return "", fmt.Errorf("media connection response contained no upload hosts")
	}
	token := base64.URLEncoding.EncodeToString(hash)
	query := url.Values{"auth": {mediaConn.Auth}, "token": {token}}
	uploadURL := url.URL{Scheme: "https", Host: mediaConn.Hosts[0].Hostname, Path: "/product/image/" + token, RawQuery: query.Encode()}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL.String(), bytes.NewReader(image))
	if err != nil {
		if urlErr, ok := err.(*url.Error); ok {
			err = urlErr.Err
		}
		return "", fmt.Errorf("prepare business product image upload: %w", err)
	}
	request.ContentLength = int64(len(image))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Origin", socket.Origin)
	request.Header.Set("Referer", socket.Origin+"/")
	response, err := cli.mediaHTTP.Do(request)
	if err != nil {
		if urlErr, ok := err.(*url.Error); ok {
			err = urlErr.Err
		}
		return "", fmt.Errorf("upload business product image: %w", err)
	}
	defer drainAndClose(response.Body)
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("business product image upload failed with status code %d", response.StatusCode)
	}
	var upload UploadResponse
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&upload); err != nil {
		return "", fmt.Errorf("decode business product image upload response: %w", err)
	}
	if upload.URL != "" {
		if err = validateBusinessMediaURL(upload.URL); err != nil {
			return "", fmt.Errorf("business product image upload returned an invalid URL: %w", err)
		}
		return upload.URL, nil
	}
	if !strings.HasPrefix(upload.DirectPath, "/") || len(upload.DirectPath) > 4096 {
		return "", fmt.Errorf("business product image upload response is missing a valid URL")
	}
	return "https://mmg.whatsapp.net" + upload.DirectPath, nil
}

const maxBusinessCoverPhotoBytes = 5 * 1024 * 1024

type businessCoverUploadResponse struct {
	MetaHMAC  string `json:"meta_hmac"`
	FBID      string `json:"fbid"`
	Timestamp string `json:"ts"`
}

var businessProfileDays = map[string]struct{}{
	"sun": {}, "mon": {}, "tue": {}, "wed": {}, "thu": {}, "fri": {}, "sat": {},
}

var businessProfileHourModes = map[string]struct{}{
	"specific_hours": {}, "open_24h": {}, "appointment_only": {},
}

func buildBusinessProfileDelta(update types.BusinessProfileUpdate) (waBinary.Node, error) {
	if update.Address == nil && update.Email == nil && update.Description == nil && update.Websites == nil && update.Hours == nil {
		return waBinary.Node{}, fmt.Errorf("business profile update is empty")
	}
	if update.Address != nil && len(*update.Address) > 512 {
		return waBinary.Node{}, fmt.Errorf("business address exceeds 512 bytes")
	}
	if update.Description != nil && len(*update.Description) > 1024 {
		return waBinary.Node{}, fmt.Errorf("business description exceeds 1024 bytes")
	}
	if update.Email != nil {
		if len(*update.Email) > 320 {
			return waBinary.Node{}, fmt.Errorf("business email exceeds 320 bytes")
		}
		if *update.Email != "" {
			parsed, err := mail.ParseAddress(*update.Email)
			if err != nil || parsed.Address != *update.Email {
				return waBinary.Node{}, fmt.Errorf("business email is invalid")
			}
		}
	}

	children := make([]waBinary.Node, 0, 7)
	if update.Address != nil {
		children = append(children, waBinary.Node{Tag: "address", Content: []byte(*update.Address)})
	}
	if update.Email != nil {
		children = append(children, waBinary.Node{Tag: "email", Content: []byte(*update.Email)})
	}
	if update.Description != nil {
		children = append(children, waBinary.Node{Tag: "description", Content: []byte(*update.Description)})
	}
	if update.Websites != nil {
		if len(*update.Websites) > 2 {
			return waBinary.Node{}, fmt.Errorf("business profile must contain at most 2 websites")
		}
		if len(*update.Websites) == 0 {
			children = append(children, waBinary.Node{Tag: "website", Content: []byte{}})
		}
		for _, website := range *update.Websites {
			if len(website) > 2048 {
				return waBinary.Node{}, fmt.Errorf("business website exceeds 2048 bytes")
			}
			parsed, err := url.ParseRequestURI(website)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return waBinary.Node{}, fmt.Errorf("business website %q is not an absolute HTTP URL", website)
			}
			children = append(children, waBinary.Node{Tag: "website", Content: []byte(website)})
		}
	}
	if update.Hours != nil {
		hours, err := buildBusinessHoursNode(*update.Hours)
		if err != nil {
			return waBinary.Node{}, err
		}
		children = append(children, hours)
	}

	return buildBusinessProfileMutationNode(children...), nil
}

func buildBusinessProfileMutationNode(children ...waBinary.Node) waBinary.Node {
	return waBinary.Node{
		Tag: "business_profile",
		Attrs: waBinary.Attrs{
			"v":             "3",
			"mutation_type": "delta",
		},
		Content: children,
	}
}

func buildBusinessHoursNode(update types.BusinessHoursUpdate) (waBinary.Node, error) {
	if update.TimeZone == "" || len(update.TimeZone) > 128 {
		return waBinary.Node{}, fmt.Errorf("business hours timezone is invalid")
	}
	if _, err := time.LoadLocation(update.TimeZone); err != nil {
		return waBinary.Node{}, fmt.Errorf("business hours timezone is invalid: %w", err)
	}
	if len(update.Days) > 7 {
		return waBinary.Node{}, fmt.Errorf("business hours must contain at most 7 days")
	}

	seen := make(map[string]struct{}, len(update.Days))
	configs := make([]waBinary.Node, 0, len(update.Days))
	for _, day := range update.Days {
		if _, ok := businessProfileDays[day.DayOfWeek]; !ok {
			return waBinary.Node{}, fmt.Errorf("invalid business hours day %q", day.DayOfWeek)
		}
		if _, ok := seen[day.DayOfWeek]; ok {
			return waBinary.Node{}, fmt.Errorf("duplicate business hours day %q", day.DayOfWeek)
		}
		seen[day.DayOfWeek] = struct{}{}
		if _, ok := businessProfileHourModes[day.Mode]; !ok {
			return waBinary.Node{}, fmt.Errorf("invalid business hours mode %q", day.Mode)
		}

		attrs := waBinary.Attrs{"day_of_week": day.DayOfWeek, "mode": day.Mode}
		if day.Mode == "specific_hours" {
			if day.OpenTime < 0 || day.OpenTime > 1439 || day.CloseTime < 0 || day.CloseTime > 1439 || day.OpenTime == day.CloseTime {
				return waBinary.Node{}, fmt.Errorf("invalid specific hours for %s", day.DayOfWeek)
			}
			attrs["open_time"] = strconv.Itoa(day.OpenTime)
			attrs["close_time"] = strconv.Itoa(day.CloseTime)
		} else if day.OpenTime != 0 || day.CloseTime != 0 {
			return waBinary.Node{}, fmt.Errorf("%s mode does not accept open or close times", day.Mode)
		}
		configs = append(configs, waBinary.Node{Tag: "business_hours_config", Attrs: attrs})
	}

	return waBinary.Node{
		Tag:     "business_hours",
		Attrs:   waBinary.Attrs{"timezone": strings.TrimSpace(update.TimeZone)},
		Content: configs,
	}, nil
}

func (cli *Client) UpdateBusinessProfile(ctx context.Context, update types.BusinessProfileUpdate) error {
	node, err := buildBusinessProfileDelta(update)
	if err != nil {
		return err
	}
	_, err = cli.sendIQ(ctx, infoQuery{
		Namespace: "w:biz",
		Type:      iqSet,
		To:        types.ServerJID,
		Content:   []waBinary.Node{node},
	})
	if err != nil {
		return fmt.Errorf("failed to update business profile: %w", err)
	}
	return nil
}

func validateBusinessCoverPhoto(image []byte) ([]byte, error) {
	if len(image) == 0 {
		return nil, fmt.Errorf("business cover photo is empty")
	}
	if len(image) > maxBusinessCoverPhotoBytes {
		return nil, fmt.Errorf("business cover photo exceeds %d bytes", maxBusinessCoverPhotoBytes)
	}
	mimeType := http.DetectContentType(image)
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		return nil, fmt.Errorf("business cover photo must be JPEG or PNG")
	}
	hash := sha256.Sum256(image)
	return hash[:], nil
}

func (cli *Client) uploadBusinessCoverPhoto(ctx context.Context, image []byte) (businessCoverUploadResponse, error) {
	var response businessCoverUploadResponse
	hash, err := validateBusinessCoverPhoto(image)
	if err != nil {
		return response, err
	}
	mediaConn, err := cli.refreshMediaConn(ctx, false)
	if err != nil {
		return response, fmt.Errorf("failed to refresh media connections: %w", err)
	}
	if len(mediaConn.Hosts) == 0 {
		return response, fmt.Errorf("media connection response contained no upload hosts")
	}

	token := base64.URLEncoding.EncodeToString(hash)
	query := url.Values{"auth": {mediaConn.Auth}, "token": {token}}
	uploadURL := url.URL{
		Scheme:   "https",
		Host:     mediaConn.Hosts[0].Hostname,
		Path:     "/pps/biz-cover-photo/" + token,
		RawQuery: query.Encode(),
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL.String(), bytes.NewReader(image))
	if err != nil {
		return response, fmt.Errorf("failed to prepare business cover photo upload: %w", err)
	}
	request.ContentLength = int64(len(image))
	request.Header.Set("Content-Type", http.DetectContentType(image))
	request.Header.Set("Origin", socket.Origin)
	request.Header.Set("Referer", socket.Origin+"/")

	httpResponse, err := cli.mediaHTTP.Do(request)
	if err != nil {
		if urlErr, ok := err.(*url.Error); ok {
			err = urlErr.Err
		}
		return response, fmt.Errorf("failed to upload business cover photo: %w", err)
	}
	defer drainAndClose(httpResponse.Body)
	if httpResponse.StatusCode != http.StatusOK {
		return response, fmt.Errorf("business cover photo upload failed with status code %d", httpResponse.StatusCode)
	}
	if err = json.NewDecoder(httpResponse.Body).Decode(&response); err != nil {
		return response, fmt.Errorf("failed to parse business cover photo upload response: %w", err)
	}
	if _, err = buildBusinessCoverPhotoUpdateNode(response); err != nil {
		return response, err
	}
	return response, nil
}

func buildBusinessCoverPhotoUpdateNode(response businessCoverUploadResponse) (waBinary.Node, error) {
	if response.MetaHMAC == "" || response.FBID == "" || response.Timestamp == "" {
		return waBinary.Node{}, fmt.Errorf("business cover photo upload response is incomplete")
	}
	return waBinary.Node{
		Tag: "cover_photo",
		Attrs: waBinary.Attrs{
			"id":    response.FBID,
			"op":    "update",
			"token": response.MetaHMAC,
			"ts":    response.Timestamp,
		},
	}, nil
}

func buildBusinessCoverPhotoDeleteNode(coverID string) (waBinary.Node, error) {
	if strings.TrimSpace(coverID) == "" {
		return waBinary.Node{}, fmt.Errorf("business cover photo ID is empty")
	}
	if len(coverID) > 256 {
		return waBinary.Node{}, fmt.Errorf("business cover photo ID exceeds 256 bytes")
	}
	return waBinary.Node{
		Tag:   "cover_photo",
		Attrs: waBinary.Attrs{"id": coverID, "op": "delete"},
	}, nil
}

func (cli *Client) SetBusinessCoverPhoto(ctx context.Context, image []byte) (string, error) {
	response, err := cli.uploadBusinessCoverPhoto(ctx, image)
	if err != nil {
		return "", err
	}
	node, err := buildBusinessCoverPhotoUpdateNode(response)
	if err != nil {
		return "", err
	}
	_, err = cli.sendIQ(ctx, infoQuery{
		Namespace: "w:biz",
		Type:      iqSet,
		To:        types.ServerJID,
		Content:   []waBinary.Node{buildBusinessProfileMutationNode(node)},
	})
	if err != nil {
		return "", fmt.Errorf("failed to set business cover photo: %w", err)
	}
	return response.FBID, nil
}

func (cli *Client) DeleteBusinessCoverPhoto(ctx context.Context, coverID string) error {
	node, err := buildBusinessCoverPhotoDeleteNode(coverID)
	if err != nil {
		return err
	}
	_, err = cli.sendIQ(ctx, infoQuery{
		Namespace: "w:biz",
		Type:      iqSet,
		To:        types.ServerJID,
		Content:   []waBinary.Node{buildBusinessProfileMutationNode(node)},
	})
	if err != nil {
		return fmt.Errorf("failed to delete business cover photo: %w", err)
	}
	return nil
}
