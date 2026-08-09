package whatsmeow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"go.mau.fi/whatsmeow/types"
)

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
