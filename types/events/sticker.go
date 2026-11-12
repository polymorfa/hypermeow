package events

import "github.com/polymorfa/hypermeow/proto/waE2E"

// StickerReupload contains every primary-device result, including NOT_FOUND and
// errors. Consumers must inspect MediaUploadResult before using StickerMessage.
// RequestID correlates this asynchronous response with SendPeerMessage's ID.
type StickerReupload struct {
	RequestID string
	Results   []*waE2E.PeerDataOperationRequestResponseMessage_PeerDataOperationResult
}
