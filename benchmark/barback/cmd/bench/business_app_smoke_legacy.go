// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

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
