package types

type BusinessLinkedAccounts struct {
	FacebookPage          *BusinessFacebookPage          `json:"facebook_page,omitempty"`
	FacebookBusiness      *BusinessFacebookBusiness      `json:"facebook_business,omitempty"`
	InstagramProfessional *BusinessInstagramProfessional `json:"instagram_professional,omitempty"`
	WhatsAppAdIdentity    *BusinessWhatsAppAdIdentity    `json:"whatsapp_ad_identity,omitempty"`
}

type BusinessFacebookPage struct {
	ID                   string `json:"id"`
	DisplayName          string `json:"display_name"`
	ProfileSync          string `json:"profile_sync,omitempty"`
	HasActiveCTWAAd      bool   `json:"has_active_ctwa_ad"`
	HasCreatedAd         bool   `json:"has_created_ad"`
	ProfilePictureURL    string `json:"profile_picture_url"`
	ShowOnProfile        bool   `json:"show_on_profile"`
	WhatsAppAsPageButton bool   `json:"whatsapp_as_page_button"`
}

type BusinessFacebookBusiness struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	CatalogID    string `json:"catalog_id,omitempty"`
	CatalogState string `json:"catalog_state,omitempty"`
}

type BusinessInstagramProfessional struct {
	Handle            string `json:"handle"`
	DisplayName       string `json:"display_name"`
	ProfilePictureURL string `json:"profile_picture_url"`
	ShowOnProfile     bool   `json:"show_on_profile"`
}

type BusinessWhatsAppAdIdentity struct {
	ID              string `json:"id"`
	HasActiveCTWAAd bool   `json:"has_active_ctwa_ad"`
	HasCreatedAd    bool   `json:"has_created_ad"`
}

type BusinessFeature string

const (
	BusinessFeatureMetaVerified      BusinessFeature = "meta_verified"
	BusinessFeatureMarketingMessages BusinessFeature = "marketing_messages"
	BusinessFeatureGenAI             BusinessFeature = "genai"
	BusinessFeatureGenAIImage        BusinessFeature = "genai_image"
	BusinessFeatureMetaOne           BusinessFeature = "meta_one"
	BusinessFeatureBBPro             BusinessFeature = "bb_pro"
)

type BusinessFeatureEligibility struct {
	Feature                 BusinessFeature `json:"feature"`
	Status                  string          `json:"status"`
	Expiration              int64           `json:"expiration,omitempty"`
	AdditionalParams        string          `json:"additional_params,omitempty"`
	ShowPrivacyInterstitial *bool           `json:"show_privacy_interstitial_to_new_users,omitempty"`
	V1Enabled               *bool           `json:"v1_enabled,omitempty"`
}

type BusinessEligibility struct {
	Features []BusinessFeatureEligibility `json:"features"`
}
