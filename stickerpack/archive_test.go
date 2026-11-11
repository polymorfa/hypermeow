package stickerpack

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/proto/waE2E"
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

func TestBuildHonorsSerializedLimit(t *testing.T) {
	limits := Limits{MaxEntries: 1, MaxFileBytes: 1, MaxTotalBytes: 1}
	for _, length := range []int{455, 456, 1024} {
		file := File{Metadata: &waE2E.StickerPackMessage_Sticker{FileName: proto.String(strings.Repeat("a", length))}, Data: []byte("x")}
		data, metadata, err := Build([]File{file}, "", nil, limits)
		if length > 455 {
			if err == nil {
				t.Fatalf("Build accepted %d-byte name producing %d serialized bytes", length, len(data))
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 1025 {
			t.Fatalf("boundary fixture has %d serialized bytes", len(data))
		}
		if _, err := Parse(data, metadata, "", limits); err != nil {
			t.Fatalf("Build output did not parse at the serialized boundary: %v", err)
		}
	}
}

// No local file bodies are needed: zip.NewReader allocates the central directory
// before Parse opens an entry or applies its own entry-count check.
func directoryFixture(records int, forgedSize bool) []byte {
	data := make([]byte, records*47+22)
	for offset := 0; offset < records*47; offset += 47 {
		binary.LittleEndian.PutUint32(data[offset:], 0x02014b50)
		binary.LittleEndian.PutUint16(data[offset+28:], 1)
		data[offset+46] = 'a'
	}
	end := data[records*47:]
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], uint16(records))
	binary.LittleEndian.PutUint16(end[10:], uint16(records))
	binary.LittleEndian.PutUint32(end[12:], uint32(records*47))
	if forgedSize {
		binary.LittleEndian.PutUint32(end[12:], 47)
	}
	return data
}

func TestParseBoundsDirectoryAllocation(t *testing.T) {
	for _, test := range []struct {
		name       string
		records    int
		forgedSize bool
	}{
		{"declared-over-limit", 4097, false},
		{"truncated-count", 65537, false},
		{"truncated-count-and-forged-size", 65537, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := directoryFixture(test.records, test.forgedSize)
			metadata := []*waE2E.StickerPackMessage_Sticker{{FileName: proto.String("a")}}
			var err error
			allocs := testing.AllocsPerRun(1, func() {
				_, err = Parse(data, metadata, "", Limits{MaxEntries: 4})
			})
			if err == nil || !strings.Contains(err.Error(), "too many entries") {
				t.Fatalf("entry limit not enforced: %v", err)
			}
			if allocs > 64 {
				t.Fatalf("rejected directory of %d entries allocated %.0f objects", test.records, allocs)
			}
			t.Logf("rejected %d directory entries with %.0f allocations", test.records, allocs)
		})
	}
}

func zip64Fixture(data []byte) []byte {
	end := len(data) - 22
	footer := bytes.Clone(data[end:])
	zip64 := make([]byte, 76)
	binary.LittleEndian.PutUint32(zip64, 0x06064b50)
	binary.LittleEndian.PutUint64(zip64[4:], 44)
	binary.LittleEndian.PutUint16(zip64[12:], 45)
	binary.LittleEndian.PutUint16(zip64[14:], 45)
	binary.LittleEndian.PutUint64(zip64[24:], uint64(binary.LittleEndian.Uint16(footer[8:])))
	binary.LittleEndian.PutUint64(zip64[32:], uint64(binary.LittleEndian.Uint16(footer[10:])))
	binary.LittleEndian.PutUint64(zip64[40:], uint64(binary.LittleEndian.Uint32(footer[12:])))
	binary.LittleEndian.PutUint64(zip64[48:], uint64(binary.LittleEndian.Uint32(footer[16:])))
	binary.LittleEndian.PutUint32(zip64[56:], 0x07064b50)
	binary.LittleEndian.PutUint64(zip64[64:], uint64(end))
	binary.LittleEndian.PutUint32(zip64[72:], 1)
	binary.LittleEndian.PutUint16(footer[8:], 0xffff)
	binary.LittleEndian.PutUint16(footer[10:], 0xffff)
	binary.LittleEndian.PutUint32(footer[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(footer[16:], 0xffffffff)
	return append(append(bytes.Clone(data[:end]), zip64...), footer...)
}

func TestDirectoryPreflightPreservesZIPVariants(t *testing.T) {
	data, metadata, err := Build([]File{{Metadata: &waE2E.StickerPackMessage_Sticker{FileName: proto.String("a.webp")}, Data: []byte("content")}}, "", nil, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	comment := bytes.Clone(data)
	binary.LittleEndian.PutUint16(comment[len(comment)-2:], 7)
	comment = append(comment, []byte("comment")...)
	smallDirectory := bytes.Clone(data)
	binary.LittleEndian.PutUint32(smallDirectory[len(data)-10:], 1)
	for _, variant := range [][]byte{data, append([]byte("prefix"), data...), comment, smallDirectory, zip64Fixture(data)} {
		got, err := Parse(variant, metadata, "", Limits{MaxEntries: 1})
		if err != nil || len(got) != 1 || string(got[0].Data) != "content" {
			t.Fatalf("supported ZIP variant failed: %v", err)
		}
	}
	for _, limits := range []Limits{
		{MaxEntries: math.MaxInt, MaxFileBytes: 1, MaxTotalBytes: 1},
		{MaxEntries: 1, MaxFileBytes: 1, MaxTotalBytes: math.MaxInt64},
	} {
		data, metadata, err := Build([]File{{Metadata: &waE2E.StickerPackMessage_Sticker{FileName: proto.String("a")}, Data: []byte("x")}}, "", nil, limits)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(data, metadata, "", limits); err != nil {
			t.Fatalf("large caller limits overflowed: %v", err)
		}
	}

	badCount := zip64Fixture(data)
	binary.LittleEndian.PutUint64(badCount[len(badCount)-98+32:], math.MaxUint64)
	if _, err := Parse(badCount, metadata, "", Limits{}); err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("unbounded ZIP64 count accepted: %v", err)
	}
	badOffset := zip64Fixture(data)
	binary.LittleEndian.PutUint64(badOffset[len(badOffset)-42+8:], math.MaxUint64)
	if _, err := Parse(badOffset, metadata, "", Limits{}); err == nil {
		t.Fatal("out-of-range ZIP64 locator accepted")
	}
}

func FuzzParse(f *testing.F) {
	data, metadata, err := Build([]File{{Metadata: &waE2E.StickerPackMessage_Sticker{FileName: proto.String("a")}, Data: []byte("x")}}, "", nil, Limits{})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add(zip64Fixture(data))
	f.Add([]byte{})
	f.Add(directoryFixture(5, true))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data, metadata, "", Limits{MaxEntries: 4, MaxFileBytes: 64, MaxTotalBytes: 256})
	})
}
