package whatsmeow

import (
	"context"
	"fmt"
	"strconv"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

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
