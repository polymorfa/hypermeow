//go:build !benchmark_legacy

package main

import "go.mau.fi/whatsmeow"

func enablePhoneConsentReceiveBarrier(client *whatsmeow.Client) {
	client.DangerousInternals().SetSynchronousMessageNameUpdates(true)
}
