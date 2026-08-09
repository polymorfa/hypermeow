package whatsmeow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

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
	if info.EntityType == "" {
		info.EntityType = types.BusinessMerchantEntityOther
	}
	if err := validateBusinessMerchantEntityType(info.EntityType); err != nil {
		return info, err
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
