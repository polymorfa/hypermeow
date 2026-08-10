//go:build benchmark_legacy

package main

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
)

func validateBusinessApp(context.Context, *whatsmeow.Client) error {
	return fmt.Errorf("business app validation requires HyperMeow")
}
