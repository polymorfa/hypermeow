package socket

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"sync/atomic"
	"testing"
)

func testAdmittedSocket(t *testing.T) *NoiseSocket {
	t.Helper()
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	key, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &NoiseSocket{fs: newTestFrameSocket(t), writeKey: key}
}

func TestNativeAdmissionDenialPreservesNonceAndHeader(t *testing.T) {
	ns := testAdmittedSocket(t)
	header := bytes.Clone(ns.fs.Header)
	denied := errors.New("revoked")
	entered := false
	err := ns.SendFrameAdmitted(t.Context(), []byte("message"), func(context.Context, func() error) error { return denied }, func() { entered = true })
	if !errors.Is(err, denied) || entered || ns.writeCounter != 0 || !bytes.Equal(ns.fs.Header, header) {
		t.Fatalf("denial changed write state: %v, %v, %d", err, entered, ns.writeCounter)
	}
}

func TestNativeAdmissionRejectsRetainedAndRepeatedCallback(t *testing.T) {
	ns := testAdmittedSocket(t)
	var retained func() error
	err := ns.SendFrameAdmitted(t.Context(), nil, func(_ context.Context, write func() error) error { retained = write; return nil }, func() { t.Error("entered after refusal") })
	if !errors.Is(err, ErrNativeAdmissionClosed) || !errors.Is(retained(), ErrNativeAdmissionClosed) || ns.writeCounter != 0 {
		t.Fatal("retained callback remained usable")
	}
	count := 0
	err = ns.SendFrameAdmitted(t.Context(), []byte("message"), func(_ context.Context, write func() error) error {
		if err := write(); err != nil {
			return err
		}
		if !errors.Is(write(), ErrNativeAdmissionClosed) {
			t.Error("second invocation accepted")
		}
		return nil
	}, func() { count++ })
	if err != nil || count != 1 || ns.writeCounter != 1 {
		t.Fatalf("single write failed: %v, count %d", err, count)
	}
}

func TestNativeAdmissionRechecksAfterWriteLock(t *testing.T) {
	ns := testAdmittedSocket(t)
	var revoked atomic.Bool
	started := make(chan struct{})
	done := make(chan error, 1)
	ns.writeLock.Lock()
	go func() {
		close(started)
		done <- ns.SendFrameAdmitted(t.Context(), nil, func(_ context.Context, write func() error) error {
			if revoked.Load() {
				return ErrNativeAdmissionClosed
			}
			return write()
		}, func() { t.Error("revoked send entered") })
	}()
	<-started
	revoked.Store(true)
	ns.writeLock.Unlock()
	if err := <-done; !errors.Is(err, ErrNativeAdmissionClosed) || ns.writeCounter != 0 {
		t.Fatalf("lock wait bypassed authority: %v", err)
	}
}

func TestNativeAdmissionCancellationAndClosedSocketNeverEnter(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "closed"}[closed], func(t *testing.T) {
			ns := testAdmittedSocket(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if closed {
				ns.fs.Close(0)
			} else {
				cancel()
			}
			err := ns.SendFrameAdmitted(ctx, nil, func(_ context.Context, write func() error) error { return write() }, func() { t.Error("non-writable socket entered") })
			if err == nil {
				t.Fatal("missing refusal")
			}
		})
	}
}

func TestNativeAdmissionEntryIsInsideWriteAndBeforeReturn(t *testing.T) {
	ns := testAdmittedSocket(t)
	order := []string{}
	err := ns.SendFrameAdmitted(t.Context(), []byte("message"), func(_ context.Context, write func() error) error {
		order = append(order, "admit")
		err := write()
		order = append(order, "written")
		return err
	}, func() { order = append(order, "entered") })
	if err != nil || len(order) != 3 || order[0] != "admit" || order[1] != "entered" || order[2] != "written" {
		t.Fatalf("wrong entry order: %v, %v", order, err)
	}
}
