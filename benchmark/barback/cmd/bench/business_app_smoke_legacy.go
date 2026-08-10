//go:build benchmark_legacy

package main

import (
	"context"

	"go.mau.fi/whatsmeow"
)

func validateBusinessApp(context.Context, *whatsmeow.Client) error {
	return nil
}
