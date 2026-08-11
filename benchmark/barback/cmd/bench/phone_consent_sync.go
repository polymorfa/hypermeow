// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

//go:build !benchmark_legacy

package main

import whatsmeow "github.com/polymorfa/hypermeow"

func enablePhoneConsentReceiveBarrier(client *whatsmeow.Client) {
	client.DangerousInternals().SetSynchronousMessageNameUpdates(true)
}
