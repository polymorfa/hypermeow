// Copyright (c) 2025 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
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
