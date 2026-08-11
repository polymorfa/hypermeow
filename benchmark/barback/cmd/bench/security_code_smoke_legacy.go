// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

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
