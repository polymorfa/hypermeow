package whatsmeow

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"go.mau.fi/whatsmeow/mex"
	"go.mau.fi/whatsmeow/types"
)

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
	if jid.IsEmpty() {
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
