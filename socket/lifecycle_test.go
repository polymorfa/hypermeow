package socket

import (
	"context"
	"errors"
	"testing"
	"time"

	waLog "github.com/polymorfa/hypermeow/util/log"
)

type finalTestCipher struct{}

func (finalTestCipher) NonceSize() int { return 12 }
func (finalTestCipher) Overhead() int  { return 0 }
func (finalTestCipher) Seal(dst, nonce, plaintext, extra []byte) []byte {
	return append(dst, plaintext...)
}
func (finalTestCipher) Open(dst, nonce, ciphertext, extra []byte) ([]byte, error) {
	return append(dst, ciphertext...), nil
}

func TestFinalSocketAsyncWriteOutlivesCancelledCaller(t *testing.T) {
	fs := NewFrameSocket(waLog.Noop, nil)
	reserved := make(chan func(), 1)
	fs.SetWorkerLauncher(func(fn func()) bool { reserved <- fn; return true })
	ns := &NoiseSocket{fs: fs, writeKey: finalTestCipher{}}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- ns.SendFrame(ctx, []byte("synthetic")) }()
	var worker func()
	select {
	case worker = <-reserved:
	case <-time.After(time.Second):
		t.Fatal("write was not registered")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller: %v", err)
	}
	// The admitted write is still pending. The owner must join this worker even
	// though SendFrame has already returned to the cancelled caller.
	worker()
	fs.SetWorkerLauncher(func(func()) bool { return false })
	if err := ns.SendFrame(context.Background(), nil); !errors.Is(err, ErrSocketClosed) {
		t.Fatalf("late write: %v", err)
	}
}

func TestFinalSocketDisconnectUnblocksPendingFrameDelivery(t *testing.T) {
	fs := NewFrameSocket(waLog.Noop, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fs.cancelCtx = ctx
	fs.incoming = []byte("synthetic")
	done := make(chan struct{})
	go func() { defer close(done); fs.frameComplete() }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("frame delivery ignored cancellation")
	}
}

func TestFinalSocketReceiveAdmissionIsSealed(t *testing.T) {
	fs := NewFrameSocket(waLog.Noop, nil)
	fs.SetWorkerLauncher(func(func()) bool { return false })
	ns, err := newNoiseSocket(context.Background(), fs, finalTestCipher{}, finalTestCipher{}, func(context.Context, []byte) { t.Error("late receive callback") }, func(context.Context, *NoiseSocket, bool) { t.Error("late disconnect callback") })
	if ns != nil || !errors.Is(err, ErrSocketClosed) {
		t.Fatalf("late receive worker admitted: %v", err)
	}
}

func TestFinalSocketStopDoesNotJoinBlockedReceiveCallback(t *testing.T) {
	fs := NewFrameSocket(waLog.Noop, nil)
	workerDone := make(chan struct{})
	fs.SetWorkerLauncher(func(fn func()) bool {
		go func() { defer close(workerDone); fn() }()
		return true
	})
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ns, err := newNoiseSocket(ctx, fs, finalTestCipher{}, finalTestCipher{}, func(context.Context, []byte) { close(entered); <-release }, func(context.Context, *NoiseSocket, bool) {})
	if err != nil {
		t.Fatal(err)
	}
	fs.Frames <- []byte("synthetic frame")
	<-entered
	ns.Stop(false, false)
	cancel()
	select {
	case <-workerDone:
		t.Fatal("socket stop claimed blocked receive completion")
	default:
	}
	close(release)
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("receive completion was not joined")
	}
}
