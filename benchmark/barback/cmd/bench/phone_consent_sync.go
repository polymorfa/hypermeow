//go:build !benchmark_legacy

package main

import whatsmeow "github.com/polymorfa/hypermeow"

func enablePhoneConsentReceiveBarrier(client *whatsmeow.Client) {
	client.DangerousInternals().SetSynchronousMessageNameUpdates(true)
}
