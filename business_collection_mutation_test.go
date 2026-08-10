package whatsmeow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/polymorfa/hypermeow/types"
)

func TestBuildCreateBusinessCollectionVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	variables, err := buildCreateBusinessCollectionVariables(jid, "  Summer tea  ", []string{"product-1", "product-2"}, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	collection := variables["input"].(map[string]any)["collection"].(map[string]any)
	if collection["name"] != "Summer tea" || collection["biz_jid"] != jid.String() || collection["catalog_session_id"] != "session-1" {
		t.Fatalf("unexpected collection: %#v", collection)
	}
	if len(collection["product_ids"].([]string)) != 2 {
		t.Fatalf("unexpected product IDs: %#v", collection)
	}
	for _, test := range []struct {
		name       string
		productIDs []string
	}{
		{"", []string{"product-1"}},
		{strings.Repeat("n", 257), []string{"product-1"}},
		{"Tea", nil},
		{"Tea", []string{"same", "same"}},
	} {
		if _, err = buildCreateBusinessCollectionVariables(jid, test.name, test.productIDs, "session-1"); err == nil {
			t.Fatalf("invalid create unexpectedly passed: %#v", test)
		}
	}
}

func TestBuildUpdateBusinessCollectionVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	name := "Tea gifts"
	variables, err := buildUpdateBusinessCollectionVariables(jid, "collection-1", types.BusinessCollectionUpdate{
		Name: &name, AddProductIDs: []string{"product-3"}, RemoveProductIDs: []string{"product-1"},
	}, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	collection := variables["input"].(map[string]any)["collection"].(map[string]any)
	if collection["id"] != "collection-1" || collection["name"] != "Tea gifts" {
		t.Fatalf("unexpected update: %#v", collection)
	}
	if collection["add"].(map[string]any)["ids"].([]string)[0] != "product-3" || collection["remove"].(map[string]any)["ids"].([]string)[0] != "product-1" {
		t.Fatalf("unexpected membership update: %#v", collection)
	}
	for _, update := range []types.BusinessCollectionUpdate{
		{},
		{AddProductIDs: []string{"same"}, RemoveProductIDs: []string{"same"}},
		{AddProductIDs: []string{"same", "same"}},
	} {
		if _, err = buildUpdateBusinessCollectionVariables(jid, "collection-1", update, "session-1"); err == nil {
			t.Fatalf("invalid update unexpectedly passed: %#v", update)
		}
	}
}

func TestBuildDeleteAndReorderBusinessCollectionsVariables(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	deleted, err := buildDeleteBusinessCollectionsVariables(jid, []string{"collection-1", "collection-2"}, "session-1")
	if err != nil || deleted["input"].(map[string]any)["collections"] == nil {
		t.Fatalf("delete = %#v, error = %v", deleted, err)
	}
	moves := []types.BusinessCollectionMove{{CollectionID: "collection-2", FromIndex: 1, ToIndex: 0}}
	reordered, err := buildReorderBusinessCollectionsVariables(jid, moves)
	if err != nil {
		t.Fatal(err)
	}
	move := reordered["input"].(map[string]any)["move"].([]map[string]any)[0]
	if move["collection_id"] != "collection-2" || move["from_index"] != 1 || move["to_index"] != 0 {
		t.Fatalf("unexpected move: %#v", move)
	}
	if _, err = buildDeleteBusinessCollectionsVariables(jid, []string{"same", "same"}, "session-1"); err == nil {
		t.Fatal("duplicate delete unexpectedly passed")
	}
	if _, err = buildReorderBusinessCollectionsVariables(jid, []types.BusinessCollectionMove{{CollectionID: "collection-1", FromIndex: -1, ToIndex: 0}}); err == nil {
		t.Fatal("negative move unexpectedly passed")
	}
}

func TestDecodeBusinessCollectionMutationResponses(t *testing.T) {
	created, err := decodeBusinessCollectionMutation(json.RawMessage(`{"xfb_whatsapp_catalog_create_collection":{"collection":{"id":"collection-1","status_info":{"status":"PENDING"}}}}`), "xfb_whatsapp_catalog_create_collection")
	if err != nil || created.ID != "collection-1" || created.ReviewStatus != "PENDING" {
		t.Fatalf("created = %#v, error = %v", created, err)
	}
	updated, err := decodeBusinessCollectionMutation(json.RawMessage(`{"xfb_whatsapp_catalog_update_collection":{"collection":{"id":"collection-1","status_info":{"status":"APPROVED"}}}}`), "xfb_whatsapp_catalog_update_collection")
	if err != nil || updated.ReviewStatus != "APPROVED" {
		t.Fatalf("updated = %#v, error = %v", updated, err)
	}
	for _, discriminator := range []string{"xfb_whatsapp_catalog_delete_collections", "xfb_whatsapp_catalog_update_collection_list"} {
		if err = decodeBusinessCatalogSuccess(json.RawMessage(`{"`+discriminator+`":{"success":true}}`), discriminator); err != nil {
			t.Fatalf("%s success failed: %v", discriminator, err)
		}
		if err = decodeBusinessCatalogSuccess(json.RawMessage(`{"`+discriminator+`":{"success":false}}`), discriminator); err == nil {
			t.Fatalf("%s false success unexpectedly passed", discriminator)
		}
	}
}
