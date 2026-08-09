package whatsmeow

import "testing"

func TestHistorySyncReceiptPolicy(t *testing.T) {
	for _, test := range []struct {
		name          string
		manual        bool
		disableManual bool
		disableAll    bool
		want          bool
	}{
		{name: "automatic", want: true},
		{name: "manual default", manual: true, want: true},
		{name: "manual disabled", manual: true, disableManual: true},
		{name: "automatic disabled", disableAll: true},
		{name: "manual globally disabled", manual: true, disableAll: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{
				ManualHistorySyncDownload:       test.manual,
				DisableManualHistorySyncReceipt: test.disableManual,
				DisableHistorySyncReceipt:       test.disableAll,
			}
			if got := client.shouldSendHistorySyncReceipt(); got != test.want {
				t.Fatalf("should send receipt = %t", got)
			}
		})
	}
}

func TestHistorySyncSideEffectsCanBeDisabledIndependently(t *testing.T) {
	client := &Client{DisableHistorySyncReceipt: true, DisableHistorySyncStorage: true, DisableHistorySyncMediaDelete: true}
	if client.shouldSendHistorySyncReceipt() {
		t.Fatal("receipt was enabled")
	}
	if client.shouldStoreHistorySync() || client.shouldDeleteHistorySyncMedia() {
		t.Fatal("history side effect was enabled")
	}
}

func TestHistorySyncDeletionKeepsCompanionNonce(t *testing.T) {
	client := &Client{DisableHistorySyncStorage: true}
	if !client.shouldStoreHistorySyncNonce() {
		t.Fatal("media deletion did not retain its companion nonce")
	}
	client.DisableHistorySyncMediaDelete = true
	if client.shouldStoreHistorySyncNonce() {
		t.Fatal("nonce storage remained enabled without storage or deletion")
	}
}
