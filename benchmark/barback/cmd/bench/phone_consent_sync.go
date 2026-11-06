// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

//go:build !benchmark_legacy

package main

import whatsmeow "github.com/polymorfa/hypermeow"

func enablePhoneConsentReceiveBarrier(client *whatsmeow.Client) {
	//lint:ignore SA1019 The benchmark requires a synchronous receive barrier to measure phone-consent persistence.
	client.DangerousInternals().SetSynchronousMessageNameUpdates(true)
}
