package whatsmeow

import (
	"context"
	"errors"
	"github.com/polymorfa/hypermeow/socket"
	"github.com/polymorfa/hypermeow/types/events"
	waLog "github.com/polymorfa/hypermeow/util/log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/store"
)

func TestFinalQRIdleCancellationClosesAndCleansHandler(t *testing.T) {
	cli := NewClient(&store.Device{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	qr, err := cli.GetQRChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, open := <-qr:
		if open {
			t.Fatal("cancelled idle channel emitted unexpected item")
		}
	case <-time.After(time.Second):
		t.Fatal("idle GetQRChannel did not close on cancellation")
	}
	if err := cli.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	cli.eventHandlersLock.RLock()
	count := len(cli.eventHandlers)
	cli.eventHandlersLock.RUnlock()
	if count != 0 {
		t.Fatalf("QR handler was not removed: %d", count)
	}
}

func finalQRItem(t *testing.T, ch <-chan QRChannelItem) QRChannelItem {
	t.Helper()
	select {
	case item, open := <-ch:
		if !open {
			t.Fatal("QR output closed before expected item")
		}
		return item
	case <-time.After(2 * time.Second):
		t.Fatal("QR output barrier timed out")
		return QRChannelItem{}
	}
}
func finalQRClosed(t *testing.T, ch <-chan QRChannelItem) {
	t.Helper()
	select {
	case _, open := <-ch:
		if open {
			t.Fatal("unexpected item after terminal")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("QR output did not close")
	}
}

func TestFinalQRFinalCloseJoinsHandlerRemoval(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	qr, err := c.GetQRChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The output may close before removal, but FinalClose must wait for the
	// reserved cleanup that is blocked by this actual handler-list reader.
	c.eventHandlersLock.RLock()
	held := true
	defer func() {
		if held {
			c.eventHandlersLock.RUnlock()
		}
	}()
	cancel()
	finalQRClosed(t, qr)
	finalTestBlocked(t, c)
	c.eventHandlersLock.RUnlock()
	held = false
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("joined cleanup: %v", err)
	}
	c.eventHandlersLock.RLock()
	count := len(c.eventHandlers)
	c.eventHandlersLock.RUnlock()
	if count != 0 {
		t.Fatalf("handler remains after join: %d", count)
	}
}

type finalQRBarrierLog struct {
	waLog.Logger
	prefix           string
	entered, release chan struct{}
	once             sync.Once
	onSub            bool
}

func (l *finalQRBarrierLog) Sub(module string) waLog.Logger {
	if l.onSub && module == "QRChannel" {
		l.once.Do(func() { close(l.entered); <-l.release })
	}
	return l
}
func (l *finalQRBarrierLog) Debugf(msg string, args ...any) {
	if strings.HasPrefix(msg, l.prefix) && !l.onSub {
		l.once.Do(func() { close(l.entered); <-l.release })
	}
}

func TestFinalQRLaunchLosesToFinalSeal(t *testing.T) {
	log := &finalQRBarrierLog{Logger: waLog.Noop, prefix: "Received QR code event", entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	qr, err := c.GetQRChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.startNative(func() { c.dispatchEvent(&events.QR{Codes: []string{"synthetic-code"}}) })
	finalTestWait(t, log.entered)
	finalTestBlocked(t, c)
	close(log.release)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	finalQRClosed(t, qr)
	if _, err := c.GetQRChannel(context.Background()); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("late QR lifetime: %v", err)
	}
}

func TestFinalQRRegistrationSealStillOwnsCleanup(t *testing.T) {
	log := &finalQRBarrierLog{Logger: waLog.Noop, onSub: true, entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	result := make(chan error, 1)
	go func() { _, err := c.GetQRChannel(context.Background()); result <- err }()
	finalTestWait(t, log.entered)
	finalTestBlocked(t, c)
	close(log.release)
	if err := <-result; !errors.Is(err, ErrClientClosed) {
		t.Fatalf("sealed registration returned ready: %v", err)
	}
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("registered cleanup: %v", err)
	}
	c.eventHandlersLock.RLock()
	count := len(c.eventHandlers)
	c.eventHandlersLock.RUnlock()
	if count != 0 {
		t.Fatalf("late registration leaked handler: %d", count)
	}
}

func TestFinalQRTerminalStatusesAndPasskeyPayloadsPreserved(t *testing.T) {
	cases := []struct {
		name  string
		event any
		want  QRChannelItem
	}{
		{"success", &events.PairSuccess{}, QRChannelSuccess},
		{"timeout", &events.Disconnected{}, QRChannelTimeout},
		{"outdated", &events.ClientOutdated{}, QRChannelClientOutdated},
		{"unexpected-connected", &events.Connected{}, QRChannelErrUnexpectedEvent},
		{"unexpected-failure", &events.ConnectFailure{}, QRChannelErrUnexpectedEvent},
		{"unexpected-logout", &events.LoggedOut{}, QRChannelErrUnexpectedEvent},
		{"unexpected-ban", &events.TemporaryBan{}, QRChannelErrUnexpectedEvent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient(&store.Device{}, nil)
			qr, err := c.GetQRChannel(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			c.dispatchEvent(tc.event)
			item := finalQRItem(t, qr)
			if item != tc.want {
				t.Fatalf("status changed: %s", item.Event)
			}
			finalQRClosed(t, qr)
			if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
				t.Fatalf("close: %v", err)
			}
		})
	}
	c := NewClient(&store.Device{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	qr, err := c.GetQRChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.dispatchEvent(&events.QRScannedWithoutMultidevice{})
	if finalQRItem(t, qr) != QRChannelScannedWithoutMultidevice {
		t.Fatal("scan status changed")
	}
	request := &events.PairPasskeyRequest{}
	c.dispatchEvent(request)
	if item := finalQRItem(t, qr); item.Event != QRChannelEventPasskeyRequest || item.PasskeyRequest != request {
		t.Fatal("passkey request payload changed")
	}
	confirmation := &events.PairPasskeyConfirmation{}
	c.dispatchEvent(confirmation)
	if item := finalQRItem(t, qr); item.Event != QRChannelEventPasskeyResponse || item.PasskeyConfirmation != confirmation {
		t.Fatal("passkey response payload changed")
	}
	failure := errors.New("synthetic pairing refusal")
	c.dispatchEvent(&events.PairPasskeyError{Error: failure})
	if item := finalQRItem(t, qr); item.Event != QRChannelEventError || item.Error != failure {
		t.Fatal("passkey error changed")
	}
	c.dispatchEvent(&events.PairError{Error: failure})
	if item := finalQRItem(t, qr); item.Event != QRChannelEventError || item.Error != failure {
		t.Fatal("pair error changed")
	}
	finalQRClosed(t, qr)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
}

func TestFinalQRReusableDisconnectAndReplacementIsolation(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	qr, err := c.GetQRChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.dispatchEvent(&events.QR{Codes: []string{"first"}})
	if item := finalQRItem(t, qr); item.Code != "first" || item.Timeout != 20*time.Second {
		t.Fatal("single-code timing changed")
	}
	c.Disconnect()
	select {
	case <-qr:
		t.Fatal("ordinary Disconnect closed active QR lifetime")
	default:
	}
	c.resetExpectedDisconnect()
	c.dispatchEvent(&events.QR{Codes: []string{"second", "3", "4", "5", "6", "7"}})
	if item := finalQRItem(t, qr); item.Code != "second" || item.Timeout != 60*time.Second {
		t.Fatal("six-code timing changed")
	}
	c.dispatchEvent(&events.PairSuccess{})
	if finalQRItem(t, qr) != QRChannelSuccess {
		t.Fatal("pair success changed")
	}
	finalQRClosed(t, qr)
	// Cleanup of the old handler must not remove a newly registered handler.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replacement, err := c.GetQRChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.dispatchEvent(&events.QRScannedWithoutMultidevice{})
	if finalQRItem(t, replacement) != QRChannelScannedWithoutMultidevice {
		t.Fatal("old cleanup removed replacement handler")
	}
	cancel()
	finalQRClosed(t, replacement)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	// A retired QR socket pointer has no effect on a replacement incarnation.
	other := NewClient(&store.Device{}, nil)
	retired, current := &socket.NoiseSocket{}, &socket.NoiseSocket{}
	other.socket = current
	other.disconnectQRSocket(retired)
	if other.socket != current || other.isExpectedDisconnect() {
		t.Fatal("retired QR cleanup changed replacement socket")
	}
	other.disconnectQRSocket(nil)
	if other.socket != current {
		t.Fatal("unbound QR cleanup changed socket")
	}
	other.socket = nil // Synthetic identity-only fixture, never connected.
	if err := other.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("other close: %v", err)
	}
}

func TestFinalQRBackpressureAndConcurrentTerminalCleanup(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	qr, err := c.GetQRChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.eventHandlersLock.RLock()
	id := c.eventHandlers[0].id
	c.eventHandlersLock.RUnlock()
	for i := 0; i < 8; i++ {
		c.dispatchEvent(&events.QRScannedWithoutMultidevice{})
	}
	c.dispatchEvent(&events.QR{Codes: []string{"backpressured"}})
	// Wait for the exact QR lifetime to close before draining: nonblocking code
	// emission must preserve the original full-buffer shutdown behavior.
	// A terminal event races the admitted emitter; both must share one close.
	done := make(chan struct{})
	go func() { c.dispatchEvent(&events.PairSuccess{}); close(done) }()
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
	finalTestWait(t, done)
	for range qr {
	}
	if c.RemoveEventHandler(id) {
		t.Fatal("owned QR handler was not removed")
	}
}

func TestFinalQRBackpressureActuallyClosesWithoutExtraCode(t *testing.T) {
	log := &finalQRBarrierLog{Logger: waLog.Noop, prefix: "Output channel didn't accept code", entered: make(chan struct{}), release: make(chan struct{})}
	c := NewClient(&store.Device{}, log)
	qr, err := c.GetQRChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		c.dispatchEvent(&events.QRScannedWithoutMultidevice{})
	}
	c.dispatchEvent(&events.QR{Codes: []string{"never-queued"}})
	finalTestWait(t, log.entered)
	close(log.release)
	count := 0
	for {
		select {
		case item, open := <-qr:
			if !open {
				if count != 8 {
					t.Fatalf("buffer count changed: %d", count)
				}
				goto drained
			}
			if item != QRChannelScannedWithoutMultidevice {
				t.Fatal("backpressured code was delivered")
			}
			count++
		case <-time.After(2 * time.Second):
			t.Fatal("backpressure cleanup did not close output")
		}
	}
drained:
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
}

func TestFinalQREmptyBatchTimesOutAndCancelledSetupRefuses(t *testing.T) {
	c := NewClient(&store.Device{}, nil)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetQRChannel(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled setup: %v", err)
	}
	qr, err := c.GetQRChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.dispatchEvent(&events.QR{})
	if finalQRItem(t, qr) != QRChannelTimeout {
		t.Fatal("empty batch timeout changed")
	}
	finalQRClosed(t, qr)
	if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
		t.Fatalf("close: %v", err)
	}
}

func TestFinalQRCancellingOldClientDoesNotAffectNewClient(t *testing.T) {
	old, fresh := NewClient(&store.Device{}, nil), NewClient(&store.Device{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldQR, err := old.GetQRChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	freshQR, err := fresh.GetQRChannel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	finalQRClosed(t, oldQR)
	fresh.dispatchEvent(&events.PairSuccess{})
	if finalQRItem(t, freshQR) != QRChannelSuccess {
		t.Fatal("old cancellation changed replacement lifetime")
	}
	finalQRClosed(t, freshQR)
	for _, c := range []*Client{old, fresh} {
		if err := c.FinalClose(finalTestContext(t)); !errors.Is(err, ErrClientShutdownIncomplete) {
			t.Fatalf("close: %v", err)
		}
	}
}
