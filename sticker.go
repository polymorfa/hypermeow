package whatsmeow

import (
	"encoding/base64"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/proto/waE2E"
)

// BuildStickerReuploadRequest requests fresh media details from the primary device.
// Send it with SendPeerMessage. Acceptance of the request does not mean the phone
// found or uploaded the stickers; results arrive in events.StickerReupload.
func (cli *Client) BuildStickerReuploadRequest(hashes [][]byte) (*waE2E.Message, error) {
	if len(hashes) == 0 {
		return nil, fmt.Errorf("at least one sticker hash is required")
	}
	req := &waE2E.PeerDataOperationRequestMessage{PeerDataOperationRequestType: waE2E.PeerDataOperationRequestType_UPLOAD_STICKER.Enum()}
	seen := make(map[string]bool)
	for _, hash := range hashes {
		if len(hash) != 32 {
			return nil, fmt.Errorf("sticker hashes must contain 32 bytes")
		}
		key := base64.StdEncoding.EncodeToString(hash)
		if !seen[key] {
			req.RequestStickerReupload = append(req.RequestStickerReupload, &waE2E.PeerDataOperationRequestMessage_RequestStickerReupload{FileSHA256: proto.String(key)})
			seen[key] = true
		}
	}
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_MESSAGE.Enum(), PeerDataOperationRequestMessage: req}}, nil
}

// BuildRecentStickerBootstrapRequest requests recents in a later history sync.
// Send with SendPeerMessage. The primary device must be available to respond.
func (cli *Client) BuildRecentStickerBootstrapRequest() *waE2E.Message {
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_PEER_DATA_OPERATION_REQUEST_MESSAGE.Enum(), PeerDataOperationRequestMessage: &waE2E.PeerDataOperationRequestMessage{PeerDataOperationRequestType: waE2E.PeerDataOperationRequestType_SEND_RECENT_STICKER_BOOTSTRAP.Enum()}}}
}
