// Package stickerpack reads and builds sticker-pack message ZIP media.
// Metadata is carried in StickerPackMessage, not in a manifest inside the ZIP.
// Evidence: whatsapp-1053201649 WAWebStickerPackUtils.js:13-40. Phone acceptance
// of newly authored archives must be verified separately from this file format.
package stickerpack

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/polymorfa/hypermeow/proto/waE2E"
)

// Limits bound untrusted archives. These are library safety limits, not claimed
// WhatsApp service limits. Zero values use DefaultLimits.
type Limits struct {
	MaxEntries                  int
	MaxFileBytes, MaxTotalBytes int64
}

var DefaultLimits = Limits{MaxEntries: 256, MaxFileBytes: 2 << 20, MaxTotalBytes: 64 << 20}

func (l Limits) normalized() (Limits, error) {
	if l.MaxEntries == 0 {
		l.MaxEntries = DefaultLimits.MaxEntries
	}
	if l.MaxFileBytes == 0 {
		l.MaxFileBytes = DefaultLimits.MaxFileBytes
	}
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = DefaultLimits.MaxTotalBytes
	}
	if l.MaxEntries < 1 || l.MaxFileBytes < 1 || l.MaxTotalBytes < 1 {
		return l, fmt.Errorf("archive limits must be positive")
	}
	return l, nil
}

// File pairs a message's item metadata with the corresponding unencrypted bytes.
type File struct {
	Metadata *waE2E.StickerPackMessage_Sticker
	Data     []byte
}

func validName(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.ContainsAny(name, "\\:\x00")
}

// Build creates a ZIP containing the sticker files and optional tray icon.
// The returned item metadata is cloned and uses the supplied file names.
func Build(files []File, trayName string, tray []byte, limits Limits) ([]byte, []*waE2E.StickerPackMessage_Sticker, error) {
	l, err := limits.normalized()
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("pack contains no stickers")
	}
	count := len(files)
	if trayName != "" {
		count++
	}
	if count > l.MaxEntries || (trayName == "") != (len(tray) == 0) {
		return nil, nil, fmt.Errorf("invalid pack entry count or tray icon")
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	seen := map[string]bool{}
	var total int64
	add := func(name string, data []byte) error {
		if !validName(name) || seen[name] {
			return fmt.Errorf("invalid or duplicate archive name")
		}
		if len(data) == 0 || int64(len(data)) > l.MaxFileBytes || int64(len(data)) > l.MaxTotalBytes-total {
			return fmt.Errorf("pack data exceeds limits or is empty")
		}
		seen[name] = true
		total += int64(len(data))
		entry, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	}
	metadata := make([]*waE2E.StickerPackMessage_Sticker, 0, len(files))
	for _, file := range files {
		if file.Metadata == nil {
			return nil, nil, fmt.Errorf("sticker metadata is required")
		}
		if err = add(file.Metadata.GetFileName(), file.Data); err != nil {
			return nil, nil, err
		}
		metadata = append(metadata, proto.Clone(file.Metadata).(*waE2E.StickerPackMessage_Sticker))
	}
	if trayName != "" {
		if err = add(trayName, tray); err != nil {
			return nil, nil, err
		}
	}
	if err = w.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), metadata, nil
}

// Parse returns listed stickers in message order, skipping the tray icon.
// It never writes to disk. Every entry is validated, including unlisted files.
func Parse(data []byte, metadata []*waE2E.StickerPackMessage_Sticker, trayName string, limits Limits) ([]File, error) {
	l, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	if len(metadata) == 0 || len(metadata) > l.MaxEntries || int64(len(data)) > l.MaxTotalBytes+int64(l.MaxEntries)*1024 {
		return nil, fmt.Errorf("invalid pack size or sticker count")
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid pack ZIP: %w", err)
	}
	if len(r.File) > l.MaxEntries {
		return nil, fmt.Errorf("pack contains too many entries")
	}
	entries := make(map[string][]byte, len(r.File))
	seen := map[string]bool{}
	var total int64
	for _, file := range r.File {
		name := strings.TrimSuffix(file.Name, "/")
		if !validName(name) || seen[name] || file.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid or duplicate archive entry")
		}
		seen[name] = true
		if file.FileInfo().IsDir() {
			continue
		}
		if file.UncompressedSize64 > uint64(l.MaxFileBytes) || file.UncompressedSize64 > uint64(l.MaxTotalBytes-total) {
			return nil, fmt.Errorf("pack exceeds decompressed size limits")
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, min(l.MaxFileBytes, l.MaxTotalBytes-total)+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("invalid pack entry: %w", readErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(content) == 0 || int64(len(content)) > l.MaxFileBytes || int64(len(content)) > l.MaxTotalBytes-total {
			return nil, fmt.Errorf("pack exceeds size limits or contains empty files")
		}
		total += int64(len(content))
		entries[file.Name] = content
	}
	out := make([]File, 0, len(metadata))
	listed := map[string]bool{}
	for _, item := range metadata {
		if item == nil || !validName(item.GetFileName()) || listed[item.GetFileName()] {
			return nil, fmt.Errorf("invalid or duplicate sticker metadata")
		}
		name := item.GetFileName()
		listed[name] = true
		if name == trayName {
			continue
		}
		content, ok := entries[name]
		if !ok {
			return nil, fmt.Errorf("sticker file is missing from archive")
		}
		out = append(out, File{Metadata: proto.Clone(item).(*waE2E.StickerPackMessage_Sticker), Data: content})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("pack contains no sticker files")
	}
	return out, nil
}
