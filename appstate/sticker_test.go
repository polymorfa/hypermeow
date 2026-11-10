package appstate

import (
	"bytes"
	"github.com/polymorfa/hypermeow/proto/waSyncAction"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestStickerPatches(t *testing.T) {
	hash := bytes.Repeat([]byte{1}, 32)
	p, err := BuildFavoriteSticker(hash, &waSyncAction.StickerAction{IsFavorite: proto.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != WAPatchRegularLow || p.Mutations[0].Version != 7 || p.Mutations[0].Index[0] != IndexFavoriteSticker {
		t.Fatal("wrong favorite patch")
	}
	if _, err = BuildFavoriteSticker(hash, &waSyncAction.StickerAction{IsFavorite: proto.Bool(true)}); err == nil {
		t.Fatal("favorite without media accepted")
	}
	p, err = BuildRemoveRecentSticker(hash, 123)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mutations[0].Value.GetRemoveRecentStickerAction().GetLastStickerSentTS() != 123 || p.Mutations[0].Version != 7 {
		t.Fatal("missing timestamp")
	}
	if _, err = BuildRemoveRecentSticker(nil, 123); err == nil {
		t.Fatal("missing hash accepted")
	}
}
