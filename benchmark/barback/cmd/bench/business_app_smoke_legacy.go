//go:build benchmark_legacy

package main

import (
	"context"

	"go.mau.fi/whatsmeow"
)

func businessAppSmokeSupported() bool {
	return false
}

func validateBusinessApp(context.Context, *whatsmeow.Client) error {
	return nil
}
