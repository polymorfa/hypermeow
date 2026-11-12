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
	// ErrGuardedSendUnsupported is returned before preparation for anything other than a
	// preallocated-ID ordinary one-to-one text message.
	ErrGuardedSendUnsupported = errors.New("guarded send requires a preallocated ordinary text DM")
	// ErrGuardedSendRefused means Admit returned without handing the node to the sender.
	// Nothing was written.
	ErrGuardedSendRefused = errors.New("guarded send refused before handoff")
	// ErrGuardedSendUncertain means the node was handed to the sender and then failed.
	// The message may have been delivered; it must never be resent automatically.
	ErrGuardedSendUncertain = errors.New("guarded send outcome uncertain after handoff")
)

// GuardAdmitTimeout bounds Admit. A slow authority check can only delay this send; no socket
// or Noise lock is held while Admit runs.
const GuardAdmitTimeout = 2 * time.Second

// guardAdmitTimeout is GuardAdmitTimeout; tests shorten it.
var guardAdmitTimeout = GuardAdmitTimeout

// GuardedSendHooks are process-local and never serialized. They run at the message-node handoff
// in sendDM, after all preparation awaits, without any socket or Noise lock held.
//
// Admit must call write at most once, synchronously, before returning. Returning without
// calling write refuses the send. Call write synchronously from Admit: a write that is not
// synchronized before Admit returns may either refuse or complete, but no write can start or
// still be running after the send call returns. sendCtx should be
// cancelled by the integrator when authority is revoked: the existing Noise writer checks it
// after taking its write lock and before encrypting, so a revocation while queued refuses the
// write without consuming a nonce.
//
// Entered is called immediately before the node is handed to the sender. It must not block.
// Everything after Entered is uncertain and must never be resent.
type GuardedSendHooks struct {
	Admit   func(ctx context.Context, id types.MessageID, write func(sendCtx context.Context) error) error
	Entered func()
}

const (
	guardedWriteOpen = iota
	guardedWriteWriting
	guardedWriteDone
	guardedWriteClosed
)

func (cli *Client) sendGuardedMessageNode(ctx context.Context, node waBinary.Node, id types.MessageID, hooks *GuardedSendHooks) ([]byte, error) {
	if hooks == nil {
		return cli.sendNodeAndGetData(ctx, node)
	}
	if hooks.Admit == nil || hooks.Entered == nil || id == "" {
		return nil, ErrGuardedSendUnsupported
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
		state    = guardedWriteOpen
		finished = make(chan struct{})
		data     []byte
		writeErr error
	)
	admitCtx, cancel := context.WithTimeout(ctx, guardAdmitTimeout)
	defer cancel()
	write := func(sendCtx context.Context) error {
		mu.Lock()
		// Check the deadline under the same lock that closes the callback, so an Admit that
		// wakes at or after the deadline cannot win the race and write.
		if state == guardedWriteOpen && admitCtx.Err() != nil {
			state = guardedWriteClosed
		}
		if state != guardedWriteOpen {
			mu.Unlock()
			return ErrGuardedSendRefused
		}
		state = guardedWriteWriting
		mu.Unlock()
		defer close(finished)
		defer func() {
			if recovered := recover(); recovered != nil {
				writeErr = fmt.Errorf("guarded send handoff panicked: %v", recovered)
			}
		}()
		hooks.Entered()
		data, writeErr = cli.sendNodeAndGetData(sendCtx, node)
		mu.Lock()
		state = guardedWriteDone
		mu.Unlock()
		return writeErr
	}

	// Admit runs in its own goroutine so the deadline is enforced even if Admit ignores its
	// context. A late Admit can no longer write: the callback is closed below.
	admitDone := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				admitDone <- fmt.Errorf("guarded send admission panicked: %v", recovered)
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
	// Only a write that actually started counts; write itself may have closed the callback at
	// the deadline without starting.
	wrote := state == guardedWriteWriting || state == guardedWriteDone
	if !wrote {
		state = guardedWriteClosed
	}
	mu.Unlock()
	if !wrote {
		if admitErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrGuardedSendRefused, admitErr)
		}
		return nil, ErrGuardedSendRefused
	}
	<-finished
	mu.Lock()
	completed := state == guardedWriteDone
	mu.Unlock()
	if !completed {
		// Entered or the handoff panicked; the node may or may not have been sent.
		return data, fmt.Errorf("%w: handoff did not complete", ErrGuardedSendUncertain)
	}
	if writeErr != nil || admitErr != nil {
		// Never expose ErrGuardedSendRefused after handoff: a caller must not mistake a possibly
		// delivered message for one that was refused.
		if writeErr != nil {
			return data, fmt.Errorf("%w: %w (admission: %v)", ErrGuardedSendUncertain, writeErr, admitErr)
		}
		return data, fmt.Errorf("%w: admission after handoff: %v", ErrGuardedSendUncertain, admitErr)
	}
	return data, nil
}

// Restrict the hook to one ordinary text payload. Unknown fields and sibling message variants
// must not carry additional send effects.
func validGuardedText(message *waE2E.Message) bool {
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
