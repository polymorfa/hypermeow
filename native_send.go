package whatsmeow

import (
	"context"
	"errors"

	"google.golang.org/protobuf/reflect/protoreflect"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/types"
)

var ErrNativeSendUnsupported = errors.New("native admission requires a preallocated ordinary text DM")

// NativeSendHooks are process-local. Admit must synchronously invoke write at
// most once under the caller's final local/authority fence. Entered is a short
// notification immediately before the actual socket write; it must not block.
// The hooks finish before SendMessage waits for the provider ACK.
type NativeSendHooks struct {
	Admit   func(context.Context, types.MessageID, func() error) error
	Entered func()
}

func (cli *Client) sendNativeMessageNode(ctx context.Context, node waBinary.Node, id types.MessageID, hooks *NativeSendHooks) ([]byte, error) {
	if hooks == nil {
		return cli.sendNodeAndGetData(ctx, node)
	}
	if hooks.Admit == nil || hooks.Entered == nil || id == "" {
		return nil, ErrNativeSendUnsupported
	}
	cli.socketLock.RLock()
	sock := cli.socket
	cli.socketLock.RUnlock()
	// A shadow relay is a separate transport, not proof of native socket entry.
	if sock == nil {
		return nil, ErrNotConnected
	}
	payload, err := waBinary.Marshal(node)
	if err != nil {
		return nil, err
	}
	err = sock.SendFrameAdmitted(ctx, payload, func(ctx context.Context, write func() error) error {
		return hooks.Admit(ctx, id, write)
	}, hooks.Entered)
	return payload, err
}

// Restrict the initial hook to one ordinary text payload. Unknown fields and
// sibling message variants must not smuggle additional native effects.
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
