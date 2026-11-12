package whatsmeow

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

func TestStickerResultPreservesHandlerFailureAndSuppressesMessage(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		for _, success := range []bool{false, true} {
			cli := NewClient(&store.Device{}, waLog.Noop)
			results, messages := 0, 0
			cli.AddEventHandlerWithSuccessStatus(func(event any) bool {
				switch e := event.(type) {
				case *events.StickerReupload:
					results++
					if e.RequestID != "request" {
						t.Errorf("request identity lost: %s", e.RequestID)
					}
					return success
				case *events.Message:
					messages++
				}
				return true
			})
			msg := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{PeerDataOperationRequestResponseMessage: &waE2E.PeerDataOperationRequestResponseMessage{PeerDataOperationRequestType: waE2E.PeerDataOperationRequestType_UPLOAD_STICKER.Enum(), StanzaID: proto.String("request")}}}
			if wrapped {
				msg = &waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{Message: msg}}
			}
			info := &types.MessageInfo{MessageSource: types.MessageSource{IsFromMe: true, Sender: types.JID{User: "fixture", Server: types.DefaultUserServer}}}
			failed := cli.handleDecryptedMessage(context.Background(), info, msg, 0)
			if failed == success {
				t.Errorf("wrapped=%t success=%t: handler failure=%t", wrapped, success, failed)
			}
			if results != 1 || messages != 0 {
				t.Errorf("wrapped=%t success=%t: results=%d ordinary messages=%d", wrapped, success, results, messages)
			}
		}
	}
}
