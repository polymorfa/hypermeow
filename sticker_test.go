package whatsmeow

import (
	"bytes"
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/appstate"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/proto/waServerSync"
	"github.com/polymorfa/hypermeow/proto/waSyncAction"
	"github.com/polymorfa/hypermeow/types/events"
)

func TestStickerPackMedia(t *testing.T) {
	// whatsapp-1053201649: WAWebBackendJobsCommon.js:105 and WAWebCryptoMediaTypeInfo.js:14-15.
	msg := &waE2E.Message{StickerPackMessage: &waE2E.StickerPackMessage{}}
	if getMediaTypeFromMessage(msg) != "sticker_pack" {
		t.Fatal("missing pack media type")
	}
	if getMediaTypeFromMessage(&waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: msg}}) != "sticker_pack" {
		t.Fatal("wrapper loses media type")
	}
	if classToThumbnailMediaType["StickerPackMessage"] != MediaStickerPackThumbnail || mediaTypeToMMSType[MediaStickerPackThumbnail] != "thumbnail-sticker-pack" {
		t.Fatal("wrong thumbnail keys/path")
	}
}
func TestStickerPeerRequests(t *testing.T) {
	cli := &Client{}
	h := bytes.Repeat([]byte{1}, 32)
	m, err := cli.BuildStickerReuploadRequest([][]byte{h, h})
	if err != nil {
		t.Fatal(err)
	}
	req := m.GetProtocolMessage().GetPeerDataOperationRequestMessage()
	if req.GetPeerDataOperationRequestType() != waE2E.PeerDataOperationRequestType_UPLOAD_STICKER || len(req.GetRequestStickerReupload()) != 1 {
		t.Fatal("wrong request")
	}
	if _, err = cli.BuildStickerReuploadRequest([][]byte{{1}}); err == nil {
		t.Fatal("bad hash accepted")
	}
	if cli.BuildRecentStickerBootstrapRequest().GetProtocolMessage().GetPeerDataOperationRequestMessage().GetPeerDataOperationRequestType() != waE2E.PeerDataOperationRequestType_SEND_RECENT_STICKER_BOOTSTRAP {
		t.Fatal("wrong bootstrap")
	}
}
func TestStickerAppStateEvents(t *testing.T) {
	cli := &Client{}
	m := appstate.Mutation{Operation: waServerSync.SyncdMutation_SET, Index: []string{appstate.IndexFavoriteSticker, "hash"}, Action: &waSyncAction.SyncActionValue{StickerAction: &waSyncAction.StickerAction{IsFavorite: proto.Bool(false)}}}
	e, ok := cli.dispatchAppState(context.Background(), appstate.WAPatchRegularLow, m, true).(*events.FavoriteSticker)
	if !ok || e.FileSHA256 != "hash" || !e.FromFullSync || e.Action.GetIsFavorite() {
		t.Fatal("wrong favorite event")
	}
	m.Action = &waSyncAction.SyncActionValue{}
	if cli.dispatchAppState(context.Background(), appstate.WAPatchRegularLow, m, true) != nil {
		t.Fatal("malformed action emitted")
	}
	m.Index = []string{appstate.IndexRemoveRecentSticker, "hash"}
	m.Action.RemoveRecentStickerAction = &waSyncAction.RemoveRecentStickerAction{LastStickerSentTS: proto.Int64(123)}
	r, ok := cli.dispatchAppState(context.Background(), appstate.WAPatchRegularLow, m, false).(*events.RemoveRecentSticker)
	if !ok || r.Action.GetLastStickerSentTS() != 123 {
		t.Fatal("wrong removal")
	}
}

func TestStickerFullSyncSelection(t *testing.T) {
	cli := &Client{EmitStickerEventsOnFullSync: true}
	for _, index := range []string{appstate.IndexFavoriteSticker, appstate.IndexRemoveRecentSticker} {
		if !cli.shouldEmitFullSyncMutation([]string{index, "hash"}) {
			t.Fatal("sticker snapshot suppressed")
		}
	}
	if cli.shouldEmitFullSyncMutation([]string{appstate.IndexMute}) {
		t.Fatal("unrelated snapshot emitted")
	}
}
