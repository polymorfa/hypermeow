//go:build benchmark_legacy

package main

import (
	"context"

	whatsmeow "github.com/polymorfa/hypermeow"
)

func businessAppSmokeSupported() bool {
	return false
}

func validateBusinessApp(context.Context, *whatsmeow.Client) error {
	return nil
}
