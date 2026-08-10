//go:build benchmark_legacy

package main

import (
	"context"

	whatsmeow "github.com/polymorfa/hypermeow"
	"github.com/polymorfa/hypermeow/types"
)

func validateIdentityVerificationCodes(context.Context, *whatsmeow.Client, types.JID) error {
	return nil
}
