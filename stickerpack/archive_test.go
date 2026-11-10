package stickerpack

import (
	"archive/zip"
	"bytes"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	files := []File{{Metadata: &waE2E.StickerPackMessage_Sticker{FileName: proto.String("b.webp"), Emojis: []string{"cat"}}, Data: []byte("second")}, {Metadata: &waE2E.StickerPackMessage_Sticker{FileName: proto.String("a.webp")}, Data: []byte("first")}}
	data, meta, err := Build(files, "tray.png", []byte("tray"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data, meta, "tray.png", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Metadata.GetFileName() != "b.webp" || string(got[1].Data) != "first" {
		t.Fatalf("wrong entries: %+v", got)
	}
	got[0].Metadata.Emojis[0] = "changed"
	if meta[0].Emojis[0] != "cat" {
		t.Fatal("metadata alias")
	}
	if _, err = Parse(data, meta, "tray.png", Limits{MaxFileBytes: 3}); err == nil {
		t.Fatal("accepted oversized entry")
	}
}
func TestMalformedArchives(t *testing.T) {
	for _, names := range [][]string{{"../a.webp"}, {"/a.webp"}, {"a.webp", "a.webp"}, {"a\\b.webp"}, {"other.webp"}} {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, name := range names {
			f, _ := w.Create(name)
			f.Write([]byte("content"))
		}
		w.Close()
		if _, err := Parse(b.Bytes(), []*waE2E.StickerPackMessage_Sticker{{FileName: proto.String("a.webp")}}, "", Limits{}); err == nil {
			t.Fatalf("accepted %v", names)
		}
	}
	data, meta, err := Build([]File{{Metadata: &waE2E.StickerPackMessage_Sticker{FileName: proto.String("a.webp")}, Data: []byte("test")}}, "", nil, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Parse(data, append(meta, meta...), "", Limits{}); err == nil {
		t.Fatal("duplicate metadata accepted")
	}
	if _, err = Parse(data[:10], meta, "", Limits{}); err == nil {
		t.Fatal("truncated ZIP accepted")
	}
}
