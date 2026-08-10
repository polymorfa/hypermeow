//go:build benchmark_legacy

package main

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

func validateIdentityVerificationCodes(context.Context, *whatsmeow.Client, types.JID) error {
	return fmt.Errorf("identity verification validation requires HyperMeow")
}
