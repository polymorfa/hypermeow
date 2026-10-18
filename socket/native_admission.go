package socket

import (
	"context"
	"errors"
	"sync"
)

var ErrNativeAdmissionClosed = errors.New("native send admission closed")

// SendFrameAdmitted joins a process-local admission to an actual frame write.
// It runs after the Noise write lock, never around goroutine scheduling or ACK
// waiting. A refusal consumes neither the Noise nonce nor the first-frame header.
func (ns *NoiseSocket) SendFrameAdmitted(ctx context.Context, plaintext []byte, admit func(context.Context, func() error) error, entered func()) error {
	if admit == nil || entered == nil {
		return ErrNativeAdmissionClosed
	}
	ns.writeLock.Lock()
	defer ns.writeLock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	ciphertext := ns.writeKey.Seal(nil, generateIV(ns.writeCounter), plaintext, nil)
	var mu sync.Mutex
	active, invoked := true, false
	var writeErr error
	err := admit(ctx, func() error {
		mu.Lock()
		defer mu.Unlock()
		if !active || invoked {
			return ErrNativeAdmissionClosed
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		invoked = true
		ns.writeCounter++
		writeErr = ns.fs.sendFrame(ciphertext, entered, ctx)
		return writeErr
	})
	mu.Lock()
	active = false
	didInvoke := invoked
	result := writeErr
	mu.Unlock()
	if !didInvoke && err == nil {
		return ErrNativeAdmissionClosed
	}
	return errors.Join(err, result)
}
