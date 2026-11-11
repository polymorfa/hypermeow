package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/types"
)

var (
	// ErrNativeSendUnsupported is returned before preparation for anything other than a
	// preallocated-ID ordinary one-to-one text message.
	ErrNativeSendUnsupported = errors.New("native admission requires a preallocated ordinary text DM")
	// ErrNativeSendRefused means Admit returned without handing the node to the sender.
	// Nothing was written.
	ErrNativeSendRefused = errors.New("native send refused before handoff")
	// ErrNativeSendUncertain means the node was handed to the sender and then failed.
	// The message may have been delivered; it must never be resent automatically.
	ErrNativeSendUncertain = errors.New("native send outcome uncertain after handoff")
)

// NativeAdmitTimeout bounds Admit. A slow authority check can only delay this send; no socket
// or Noise lock is held while Admit runs.
const NativeAdmitTimeout = 2 * time.Second

// nativeAdmitTimeout is NativeAdmitTimeout; tests shorten it.
var nativeAdmitTimeout = NativeAdmitTimeout

// NativeSendHooks are process-local and never serialized. They run at the message-node handoff
// in sendDM, after all preparation awaits, without any socket or Noise lock held.
//
// Admit must call write at most once, synchronously, before returning. Returning without
// calling write refuses the send. write refuses once Admit has returned. sendCtx should be
// cancelled by the integrator when authority is revoked: the existing Noise writer checks it
// after taking its write lock and before encrypting, so a revocation while queued refuses the
// write without consuming a nonce.
//
// Entered is called immediately before the node is handed to the sender. It must not block.
// Everything after Entered is uncertain and must never be resent.
type NativeSendHooks struct {
	Admit   func(ctx context.Context, id types.MessageID, write func(sendCtx context.Context) error) error
	Entered func()
}

const (
	nativeWriteOpen = iota
	nativeWriteWriting
	nativeWriteDone
	nativeWriteClosed
)

func (cli *Client) sendNativeMessageNode(ctx context.Context, node waBinary.Node, id types.MessageID, hooks *NativeSendHooks) ([]byte, error) {
	if hooks == nil {
		return cli.sendNodeAndGetData(ctx, node)
	}
	if hooks.Admit == nil || hooks.Entered == nil || id == "" {
		return nil, ErrNativeSendUnsupported
	}
	cli.socketLock.RLock()
	connected := cli.socket != nil
	cli.socketLock.RUnlock()
	// A shadow relay is a separate transport and is not admitted here.
	if !connected && cli.testSendHook == nil {
		return nil, ErrNotConnected
	}

	var (
		mu       sync.Mutex
		state    = nativeWriteOpen
		finished = make(chan struct{})
		data     []byte
		writeErr error
	)
	write := func(sendCtx context.Context) error {
		mu.Lock()
		if state != nativeWriteOpen {
			mu.Unlock()
			return ErrNativeSendRefused
		}
		state = nativeWriteWriting
		mu.Unlock()
		defer close(finished)
		defer func() {
			if recovered := recover(); recovered != nil {
				writeErr = fmt.Errorf("native handoff panicked: %v", recovered)
			}
		}()
		hooks.Entered()
		data, writeErr = cli.sendNodeAndGetData(sendCtx, node)
		mu.Lock()
		state = nativeWriteDone
		mu.Unlock()
		return writeErr
	}

	// Admit runs in its own goroutine so the deadline is enforced even if Admit ignores its
	// context. A late Admit can no longer write: the callback is closed below.
	admitCtx, cancel := context.WithTimeout(ctx, nativeAdmitTimeout)
	defer cancel()
	admitDone := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				admitDone <- fmt.Errorf("native admission panicked: %v", recovered)
			}
		}()
		admitDone <- hooks.Admit(admitCtx, id, write)
	}()
	var admitErr error
	select {
	case admitErr = <-admitDone:
	case <-admitCtx.Done():
		admitErr = admitCtx.Err()
	}

	// Close the callback. A write that already started is waited for, so no write can begin
	// or still be in flight after this function returns.
	mu.Lock()
	wrote := state != nativeWriteOpen
	if !wrote {
		state = nativeWriteClosed
	}
	mu.Unlock()
	if !wrote {
		if admitErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrNativeSendRefused, admitErr)
		}
		return nil, ErrNativeSendRefused
	}
	<-finished
	mu.Lock()
	completed := state == nativeWriteDone
	mu.Unlock()
	if !completed {
		// Entered or the handoff panicked; the node may or may not have been sent.
		return data, fmt.Errorf("%w: handoff did not complete", ErrNativeSendUncertain)
	}
	if writeErr != nil || admitErr != nil {
		return data, fmt.Errorf("%w: %w", ErrNativeSendUncertain, errors.Join(writeErr, admitErr))
	}
	return data, nil
}

// Restrict the hook to one ordinary text payload. Unknown fields and sibling message variants
// must not carry additional native effects.
func validNativeText(message *waE2E.Message) bool {
	if message == nil || message.Conversation == nil || len(message.ProtoReflect().GetUnknown()) != 0 {
		return false
	}
	valid := true
	message.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if field.Name() != "conversation" {
			valid = false
		}
		return valid
	})
	return valid
}
