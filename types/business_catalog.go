// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package types

type BusinessCatalogPage struct {
	Next     string            `json:"next,omitempty"`
	Previous string            `json:"previous,omitempty"`
	Products []BusinessProduct `json:"products"`
}

type BusinessProduct struct {
	ID                 string                  `json:"id"`
	RetailerID         string                  `json:"retailer_id,omitempty"`
	BelongsTo          string                  `json:"belongs_to,omitempty"`
	Name               string                  `json:"name"`
	Description        string                  `json:"description,omitempty"`
	Price              string                  `json:"price"`
	Currency           string                  `json:"currency"`
	URL                string                  `json:"url,omitempty"`
	ShimmedURL         string                  `json:"shimmed_url,omitempty"`
	Hidden             bool                    `json:"is_hidden"`
	Sanctioned         bool                    `json:"is_sanctioned"`
	MaxAvailable       int                     `json:"max_available,omitempty"`
	Availability       string                  `json:"product_availability,omitempty"`
	ComplianceCategory string                  `json:"compliance_category,omitempty"`
	Compliance         *BusinessComplianceInfo `json:"compliance_info,omitempty"`
	Media              BusinessProductMedia    `json:"media"`
	SalePrice          *BusinessSalePrice      `json:"sale_price,omitempty"`
	Status             BusinessProductStatus   `json:"status_info"`
	VariantInfo        *BusinessProductVariant `json:"variant_info,omitempty"`
}

type BusinessProductInput struct {
	Name               string                  `json:"name"`
	Description        string                  `json:"description,omitempty"`
	Currency           string                  `json:"currency,omitempty"`
	Price              string                  `json:"price,omitempty"`
	SalePrice          string                  `json:"sale_price,omitempty"`
	URL                string                  `json:"url,omitempty"`
	RetailerID         string                  `json:"retailer_id,omitempty"`
	Hidden             bool                    `json:"is_hidden"`
	ImageURLs          []string                `json:"image_urls"`
	VideoURLs          []string                `json:"video_urls,omitempty"`
	ComplianceCategory string                  `json:"compliance_category,omitempty"`
	Compliance         *BusinessComplianceInfo `json:"compliance_info,omitempty"`
}

type BusinessComplianceInfo struct {
	CountryCodeOrigin string           `json:"country_code_origin,omitempty"`
	ImporterName      string           `json:"importer_name,omitempty"`
	ImporterAddress   *BusinessAddress `json:"importer_address,omitempty"`
}

type BusinessMerchantEntityType string

const (
	BusinessMerchantEntitySoleProprietorship          BusinessMerchantEntityType = "SOLE_PROPRIETORSHIP"
	BusinessMerchantEntityPartnership                 BusinessMerchantEntityType = "PARTNERSHIP"
	BusinessMerchantEntityPrivateCompany              BusinessMerchantEntityType = "PRIVATE_COMPANY"
	BusinessMerchantEntityPublicCompany               BusinessMerchantEntityType = "PUBLIC_COMPANY"
	BusinessMerchantEntityLimitedLiabilityPartnership BusinessMerchantEntityType = "LIMITED_LIABILITY_PARTNERSHIP"
	BusinessMerchantEntityOther                       BusinessMerchantEntityType = "OTHER"
)

type BusinessMerchantContact struct {
	Email          string `json:"email"`
	LandlineNumber string `json:"landline_number"`
	MobileNumber   string `json:"mobile_number"`
}

type BusinessMerchantOfficer struct {
	Name           string `json:"name"`
	Email          string `json:"email"`
	LandlineNumber string `json:"landline_number"`
	MobileNumber   string `json:"mobile_number"`
}

type BusinessMerchantCompliance struct {
	EntityName       string                     `json:"entity_name"`
	EntityType       BusinessMerchantEntityType `json:"entity_type"`
	IsRegistered     bool                       `json:"is_registered"`
	EntityTypeCustom string                     `json:"entity_type_custom"`
	CustomerCare     BusinessMerchantContact    `json:"customer_care_details"`
	GrievanceOfficer BusinessMerchantOfficer    `json:"grievance_officer_details"`
}

type BusinessAddress struct {
	Street1     string `json:"street1,omitempty"`
	Street2     string `json:"street2,omitempty"`
	City        string `json:"city,omitempty"`
	Region      string `json:"region,omitempty"`
	PostalCode  string `json:"postal_code,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

type BusinessProductMedia struct {
	Images []BusinessProductImage `json:"images,omitempty"`
	Videos []BusinessProductVideo `json:"videos,omitempty"`
}

type BusinessProductImage struct {
	ID          string `json:"id"`
	OriginalURL string `json:"original_image_url,omitempty"`
	RequestURL  string `json:"request_image_url,omitempty"`
}

type BusinessProductVideo struct {
	ID           string `json:"id"`
	OriginalURL  string `json:"original_video_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
}

type BusinessSalePrice struct {
	Price     string `json:"price"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

type BusinessProductStatus struct {
	Status    string `json:"status,omitempty"`
	CanAppeal bool   `json:"can_appeal,omitempty"`
}

type BusinessProductVariant struct {
	Availability      BusinessVariantAvailability `json:"availability,omitempty"`
	ListingDetails    BusinessVariantListing      `json:"listing_details,omitempty"`
	Types             []BusinessVariantType       `json:"types,omitempty"`
	VariantProperties []BusinessVariantProperty   `json:"variant_properties,omitempty"`
}

type BusinessVariantAvailability struct {
	Listings []BusinessVariantAvailabilityItem `json:"listing,omitempty"`
}

type BusinessVariantAvailabilityItem struct {
	ProductID string                    `json:"product_id,omitempty"`
	Available bool                      `json:"is_available"`
	Options   []BusinessVariantProperty `json:"options,omitempty"`
}

type BusinessVariantListing struct {
	Description string `json:"description,omitempty"`
	LowestPrice string `json:"lowest_price,omitempty"`
	MultiPrice  string `json:"multi_price,omitempty"`
}

type BusinessVariantType struct {
	Name    string                  `json:"name"`
	Options []BusinessVariantOption `json:"options,omitempty"`
}

type BusinessVariantOption struct {
	Value     string                    `json:"value"`
	Thumbnail *BusinessVariantThumbnail `json:"thumbnail_media,omitempty"`
}

type BusinessVariantThumbnail struct {
	ID                 string             `json:"id,omitempty"`
	OriginalURL        string             `json:"original_image_url,omitempty"`
	RequestURL         string             `json:"request_image_url,omitempty"`
	OriginalDimensions BusinessDimensions `json:"original_dimensions,omitempty"`
}

type BusinessDimensions struct {
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

type BusinessVariantProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type BusinessCollectionPage struct {
	Next        string               `json:"next,omitempty"`
	Collections []BusinessCollection `json:"collections"`
}

type BusinessCollection struct {
	ID       string                   `json:"id"`
	Name     string                   `json:"name"`
	Next     string                   `json:"next,omitempty"`
	Previous string                   `json:"previous,omitempty"`
	Products []BusinessProduct        `json:"products"`
	Status   BusinessCollectionStatus `json:"status_info"`
}

type BusinessCollectionStatus struct {
	Status       string `json:"status,omitempty"`
	CanAppeal    bool   `json:"can_appeal,omitempty"`
	CommerceURL  string `json:"commerce_url,omitempty"`
	RejectReason string `json:"reject_reason,omitempty"`
}

type BusinessCollectionUpdate struct {
	Name             *string  `json:"name,omitempty"`
	AddProductIDs    []string `json:"add_product_ids,omitempty"`
	RemoveProductIDs []string `json:"remove_product_ids,omitempty"`
}

type BusinessCollectionMutationResult struct {
	ID           string `json:"id"`
	ReviewStatus string `json:"review_status"`
}

type BusinessCollectionMove struct {
	CollectionID string `json:"collection_id"`
	FromIndex    int    `json:"from_index"`
	ToIndex      int    `json:"to_index"`
}
