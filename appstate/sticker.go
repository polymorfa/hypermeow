package appstate

import (
	"encoding/base64"
	"fmt"

	"github.com/polymorfa/hypermeow/proto/waSyncAction"
	"google.golang.org/protobuf/proto"
)

// BuildFavoriteSticker builds a regular_low v7 favorite/unfavorite mutation.
// The hash is the plaintext SHA-256. Favoriting requires downloadable media details.
// Evidence: WAWebStickersFavoriteSyncAction, whatsapp-1053201649, lines 20-25, 81-99.
func BuildFavoriteSticker(hash []byte, action *waSyncAction.StickerAction) (PatchInfo, error) {
	if len(hash) != 32 || action == nil || action.IsFavorite == nil {
		return PatchInfo{}, fmt.Errorf("sticker hash and favorite state are required")
	}
	if action.GetIsFavorite() && (len(action.MediaKey) != 32 || len(action.FileEncSHA256) != 32 || action.GetDirectPath() == "" || action.GetMimetype() == "") {
		return PatchInfo{}, fmt.Errorf("favorite sticker requires media details")
	}
	return PatchInfo{Type: WAPatchRegularLow, Mutations: []MutationInfo{{Index: []string{IndexFavoriteSticker, base64.StdEncoding.EncodeToString(hash)}, Version: 7, Value: &waSyncAction.SyncActionValue{StickerAction: proto.Clone(action).(*waSyncAction.StickerAction)}}}}, nil
}

// BuildRemoveRecentSticker removes a recent sticker up to lastSentTimestampMS.
// The timestamp guard preserves a sticker sent again after the removal was made.
// Evidence: WAWebStickersRemoveRecentSyncAction, whatsapp-1053201649, lines 16-21, 35-38, 46-55.
func BuildRemoveRecentSticker(hash []byte, lastSentTimestampMS int64) (PatchInfo, error) {
	if len(hash) != 32 || lastSentTimestampMS < 0 {
		return PatchInfo{}, fmt.Errorf("valid sticker hash and last sent timestamp are required")
	}
	return PatchInfo{Type: WAPatchRegularLow, Mutations: []MutationInfo{{Index: []string{IndexRemoveRecentSticker, base64.StdEncoding.EncodeToString(hash)}, Version: 7, Value: &waSyncAction.SyncActionValue{RemoveRecentStickerAction: &waSyncAction.RemoveRecentStickerAction{LastStickerSentTS: proto.Int64(lastSentTimestampMS)}}}}}, nil
}
