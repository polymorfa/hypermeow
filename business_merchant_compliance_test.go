package whatsmeow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

type merchantComplianceRoundTripper func(*http.Request) (*http.Response, error)

func (fn merchantComplianceRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func syntheticMerchantCompliance() types.BusinessMerchantCompliance {
	return types.BusinessMerchantCompliance{
		EntityName:       "Polymorfa Labs",
		EntityType:       types.BusinessMerchantEntityPrivateCompany,
		IsRegistered:     true,
		EntityTypeCustom: "",
		CustomerCare: types.BusinessMerchantContact{
			Email:          "support@example.test",
			LandlineNumber: "+961 1 555 0100",
			MobileNumber:   "+961 70 555 010",
		},
		GrievanceOfficer: types.BusinessMerchantOfficer{
			Name:           "Compliance Desk",
			Email:          "appeals@example.test",
			LandlineNumber: "+961 1 555 0101",
			MobileNumber:   "+961 70 555 011",
		},
	}
}

func TestBuildBusinessMerchantComplianceVariables(t *testing.T) {
	got, err := buildBusinessMerchantComplianceVariables(types.NewJID("15550001111", types.DefaultUserServer), syntheticMerchantCompliance())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"input": map[string]any{
		"biz_jid": "15550001111@s.whatsapp.net",
		"merchant_info": map[string]any{
			"entity_name":        "Polymorfa Labs",
			"entity_type":        "PRIVATE_COMPANY",
			"is_registered":      true,
			"entity_type_custom": "",
			"customer_care_details": map[string]any{
				"email": "support@example.test", "landline_number": "+961 1 555 0100", "mobile_number": "+961 70 555 010",
			},
			"grievance_officer_details": map[string]any{
				"name": "Compliance Desk", "email": "appeals@example.test", "landline_number": "+961 1 555 0101", "mobile_number": "+961 70 555 011",
			},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected variables:\n got %#v\nwant %#v", got, want)
	}
}

func TestBuildBusinessMerchantComplianceQueryVariables(t *testing.T) {
	got, err := buildBusinessMerchantComplianceQueryVariables(types.NewJID("15550001111", types.DefaultUserServer))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"request": map[string]any{"biz_jid": "15550001111@s.whatsapp.net"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected variables: got %#v want %#v", got, want)
	}
}

func TestDecodeBusinessMerchantCompliance(t *testing.T) {
	data := json.RawMessage(`{"xfb_whatsapp_biz_merchant_compliance_info":{"merchant_info":{"entity_name":"Polymorfa Labs","entity_type":"PRIVATE_COMPANY","is_registered":true,"entity_type_custom":"","customer_care_details":{"email":"support@example.test","landline_number":"+961 1 555 0100","mobile_number":"+961 70 555 010"},"grievance_officer_details":{"name":"Compliance Desk","email":"appeals@example.test","landline_number":"+961 1 555 0101","mobile_number":"+961 70 555 011"}}}}`)
	got, err := decodeBusinessMerchantCompliance(data, "xfb_whatsapp_biz_merchant_compliance_info")
	if err != nil {
		t.Fatal(err)
	}
	want := syntheticMerchantCompliance()
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("unexpected compliance response: got %#v want %#v", *got, want)
	}
}

func TestBusinessMerchantComplianceRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.BusinessMerchantCompliance)
	}{
		{name: "entity type", mutate: func(info *types.BusinessMerchantCompliance) { info.EntityType = "COOPERATIVE" }},
		{name: "missing entity type", mutate: func(info *types.BusinessMerchantCompliance) { info.EntityType = "" }},
		{name: "missing custom entity type", mutate: func(info *types.BusinessMerchantCompliance) {
			info.EntityType = types.BusinessMerchantEntityOther
			info.EntityTypeCustom = "   "
		}},
		{name: "empty entity name", mutate: func(info *types.BusinessMerchantCompliance) { info.EntityName = "   " }},
		{name: "entity name length", mutate: func(info *types.BusinessMerchantCompliance) { info.EntityName = strings.Repeat("n", 257) }},
		{name: "customer email length", mutate: func(info *types.BusinessMerchantCompliance) { info.CustomerCare.Email = strings.Repeat("e", 255) }},
		{name: "officer phone length", mutate: func(info *types.BusinessMerchantCompliance) {
			info.GrievanceOfficer.MobileNumber = strings.Repeat("1", 65)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := syntheticMerchantCompliance()
			tc.mutate(&info)
			if _, err := buildBusinessMerchantComplianceVariables(types.NewJID("15550001111", types.DefaultUserServer), info); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDecodeBusinessMerchantComplianceRejectsMissingPayload(t *testing.T) {
	if _, err := decodeBusinessMerchantCompliance(json.RawMessage(`{"xfb_whatsapp_biz_merchant_compliance_info":{}}`), "xfb_whatsapp_biz_merchant_compliance_info"); err == nil {
		t.Fatal("expected missing merchant_info error")
	}
}

func TestBusinessMerchantComplianceMethodsUseMatchingGraphEnvironments(t *testing.T) {
	jid := types.NewJID("15550001111", types.DefaultUserServer)
	client := NewClient(&store.Device{ID: &jid}, waLog.Noop)
	client.getBusinessCatalogAuth().token = businessAccessToken{accessToken: "synthetic-ad-token", actorID: "synthetic-actor"}
	client.mediaHTTP = &http.Client{Transport: merchantComplianceRoundTripper(func(request *http.Request) (*http.Response, error) {
		var body struct {
			AccessToken string         `json:"access_token"`
			DocumentID  string         `json:"doc_id"`
			Variables   map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		var payload string
		switch body.DocumentID {
		case businessGetMerchantComplianceDocumentID:
			if request.URL.String() != businessCatalogGraphQLEndpoint || body.AccessToken != businessCatalogGraphQLAccessToken || body.Variables["request"] == nil {
				return nil, fmt.Errorf("unexpected catalog query: %s %#v", request.URL, body)
			}
			payload = `{"data":{"xfb_whatsapp_biz_merchant_compliance_info":{"merchant_info":{"entity_name":"Polymorfa Labs","entity_type":"PRIVATE_COMPANY","is_registered":true,"entity_type_custom":"","customer_care_details":{},"grievance_officer_details":{}}}}}`
		case businessSetMerchantComplianceDocumentID:
			input, _ := body.Variables["input"].(map[string]any)
			if request.URL.String() != businessGraphQLEndpoint || body.AccessToken != "synthetic-ad-token" || input["actor_id"] != "synthetic-actor" {
				return nil, fmt.Errorf("unexpected Facebook mutation: %s %#v", request.URL, body)
			}
			payload = `{"data":{"xfb_whatsapp_biz_merchant_set_compliance_info":{"merchant_info":{"entity_name":"Polymorfa Labs","entity_type":"PRIVATE_COMPANY","is_registered":true,"entity_type_custom":"","customer_care_details":{},"grievance_officer_details":{}}}}}`
		default:
			return nil, fmt.Errorf("unexpected document ID %q", body.DocumentID)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(payload))}, nil
	})}

	read, err := client.GetBusinessMerchantCompliance(context.Background())
	if err != nil || read.EntityName != "Polymorfa Labs" {
		t.Fatalf("read = %#v, error = %v", read, err)
	}
	updated, err := client.SetBusinessMerchantCompliance(context.Background(), syntheticMerchantCompliance())
	if err != nil || updated.EntityType != types.BusinessMerchantEntityPrivateCompany {
		t.Fatalf("updated = %#v, error = %v", updated, err)
	}
}
