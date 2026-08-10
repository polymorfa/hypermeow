package whatsmeow

import (
	"bytes"
	"compress/zlib"
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type historySyncDeviceContainer struct {
	putContextErr chan error
	putRelease    chan struct{}
}

func (container *historySyncDeviceContainer) PutDevice(ctx context.Context, _ *store.Device) error {
	container.putContextErr <- ctx.Err()
	if container.putRelease != nil {
		<-container.putRelease
	}
	return nil
}

func (*historySyncDeviceContainer) DeleteDevice(context.Context, *store.Device) error { return nil }

type orderedHistorySyncDeviceContainer struct {
	lock            sync.Mutex
	calls           int
	persisted       string
	firstEntered    chan struct{}
	secondEntered   chan struct{}
	secondCommitted chan struct{}
	committed       chan struct{}
}

func (container *orderedHistorySyncDeviceContainer) PutDevice(_ context.Context, device *store.Device) error {
	container.lock.Lock()
	container.calls++
	call := container.calls
	nonce := device.CompanionMetaNonce
	container.lock.Unlock()

	if call == 1 {
		close(container.firstEntered)
		select {
		case <-container.secondEntered:
			<-container.secondCommitted
		case <-time.After(time.Second):
		}
	} else {
		close(container.secondEntered)
	}

	container.lock.Lock()
	container.persisted = nonce
	container.lock.Unlock()
	if call == 2 {
		close(container.secondCommitted)
	}
	container.committed <- struct{}{}
	return nil
}

func (*orderedHistorySyncDeviceContainer) DeleteDevice(context.Context, *store.Device) error {
	return nil
}

func (container *orderedHistorySyncDeviceContainer) persistedNonce() string {
	container.lock.Lock()
	defer container.lock.Unlock()
	return container.persisted
}

func historySyncNotificationWithNonce(t *testing.T, nonce string) *waE2E.HistorySyncNotification {
	t.Helper()
	syncType := waHistorySync.HistorySync_INITIAL_BOOTSTRAP
	historyBytes, err := proto.Marshal(&waHistorySync.HistorySync{
		SyncType:           &syncType,
		CompanionMetaNonce: proto.String(nonce),
	})
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err = writer.Write(historyBytes); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &waE2E.HistorySyncNotification{InitialHistBootstrapInlinePayload: compressed.Bytes()}
}

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

func TestAsyncHistorySyncNoncePersistenceIgnoresCallerCancellation(t *testing.T) {
	container := &historySyncDeviceContainer{putContextErr: make(chan error, 1)}
	client := &Client{
		DisableHistorySyncStorage: true,
		Log:                       waLog.Noop,
		Store:                     &store.Device{Container: container},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.DownloadHistorySync(ctx, historySyncNotificationWithNonce(t, "fresh"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-container.putContextErr; err != nil {
		t.Fatalf("nonce persistence inherited caller cancellation: %v", err)
	}
}

func TestAsyncHistorySyncNoncePersistenceDoesNotBlockDownload(t *testing.T) {
	container := &historySyncDeviceContainer{
		putContextErr: make(chan error, 1),
		putRelease:    make(chan struct{}),
	}
	t.Cleanup(func() { close(container.putRelease) })
	client := &Client{
		DisableHistorySyncStorage: true,
		Log:                       waLog.Noop,
		Store:                     &store.Device{Container: container},
	}
	notification := historySyncNotificationWithNonce(t, "fresh")
	done := make(chan error, 1)
	go func() {
		_, err := client.DownloadHistorySync(context.Background(), notification, false)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("asynchronous nonce persistence blocked history sync download")
	}
	if nonce := client.currentCompanionMetaNonce(); nonce != "fresh" {
		t.Fatalf("companion meta nonce = %q", nonce)
	}
	select {
	case err := <-container.putContextErr:
		if err != nil {
			t.Fatalf("nonce persistence inherited caller cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("asynchronous nonce persistence did not start")
	}
}

func TestAsyncHistorySyncNoncePersistenceKeepsNewestNonce(t *testing.T) {
	container := &orderedHistorySyncDeviceContainer{
		firstEntered:    make(chan struct{}),
		secondEntered:   make(chan struct{}),
		secondCommitted: make(chan struct{}),
		committed:       make(chan struct{}, 2),
	}
	client := &Client{
		DisableHistorySyncStorage: true,
		Log:                       waLog.Noop,
		Store:                     &store.Device{Container: container},
	}
	if _, err := client.DownloadHistorySync(context.Background(), historySyncNotificationWithNonce(t, "older"), false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-container.firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first nonce persistence did not start")
	}
	if _, err := client.DownloadHistorySync(context.Background(), historySyncNotificationWithNonce(t, "newest"), false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-container.committed:
		case <-time.After(2 * time.Second):
			t.Fatal("nonce persistence did not complete")
		}
	}
	if nonce := container.persistedNonce(); nonce != "newest" {
		t.Fatalf("persisted companion meta nonce = %q", nonce)
	}
}
