package whatsmeow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

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
