// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

//go:build !benchmark_legacy

package main

import (
	"context"
	"fmt"

	whatsmeow "github.com/polymorfa/hypermeow"
	"github.com/polymorfa/hypermeow/types"
)

func businessAppSmokeSupported() bool {
	return true
}

func validateBusinessApp(ctx context.Context, client *whatsmeow.Client) error {
	business := types.NewJID("15551234567", types.DefaultUserServer)
	catalog, err := client.GetCatalog(ctx, business, whatsmeow.GetCatalogParams{Limit: 50})
	if err != nil {
		return fmt.Errorf("business app catalog: %w", err)
	}
	if len(catalog.Products) != 2 || catalog.Products[0].ID != "p-tea" || catalog.Products[0].Price != "1250" || catalog.Products[0].Currency != "USD" {
		return fmt.Errorf("business app catalog returned an unexpected fixture")
	}
	product, err := client.GetCatalogProduct(ctx, business, "p-tea")
	if err != nil {
		return fmt.Errorf("business app product: %w", err)
	}
	if product.ID != "p-tea" || product.RetailerID != "sku-tea-20" {
		return fmt.Errorf("business app product returned an unexpected fixture")
	}
	collections, err := client.GetProductCollections(ctx, business, whatsmeow.GetCollectionsParams{})
	if err != nil {
		return fmt.Errorf("business app collections: %w", err)
	}
	if len(collections.Collections) != 1 || collections.Collections[0].ID != "c-seasonal" {
		return fmt.Errorf("business app collections returned an unexpected fixture")
	}
	collection, err := client.GetProductCollection(ctx, business, "c-seasonal", whatsmeow.GetCatalogParams{Limit: 50})
	if err != nil {
		return fmt.Errorf("business app collection: %w", err)
	}
	if collection.ID != "c-seasonal" || len(collection.Products) != 2 {
		return fmt.Errorf("business app collection returned an unexpected fixture")
	}
	products, err := client.GetCatalogProducts(ctx, business, []string{"p-coffee", "p-tea"})
	if err != nil {
		return fmt.Errorf("business app product list: %w", err)
	}
	if len(products) != 2 || products[0].ID != "p-coffee" || products[1].ID != "p-tea" {
		return fmt.Errorf("business app product list returned an unexpected fixture")
	}
	order, err := client.GetOrderDetails(ctx, "o-100", "synthetic-token")
	if err != nil {
		return fmt.Errorf("business app order: %w", err)
	}
	if order.ID != "o-100" || order.Price.Total != 2650 || len(order.Products) != 2 {
		return fmt.Errorf("business app order returned an unexpected fixture")
	}
	return nil
}
