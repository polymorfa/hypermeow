package stickerpack

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"math"
)

var errTooManyEntries = errors.New("pack contains too many entries")

type directoryInfo struct {
	end                   int
	records, size, offset uint64
}

// preflightDirectory bounds both zip.NewReader's declared-count preallocation
// and its actual directory scan. ZIP counts can wrap at 65536, so neither the
// declared count nor the declared directory size alone bounds that scan.
func preflightDirectory(data []byte, maxEntries int) error {
	d, err := findDirectory(data)
	if err != nil {
		return err
	}
	if d.records > uint64(maxEntries) {
		return errTooManyEntries
	}
	if d.size > uint64(d.end) || d.offset > math.MaxInt64 {
		return zip.ErrFormat
	}
	start := d.end - int(d.size)
	if err := countDirectory(data, start, maxEntries); err != nil {
		return err
	}
	// archive/zip may prefer the nominal offset when a prepended archive or an
	// inaccurate directory size implies a positive base offset. Bound that path
	// too, without allocating names, extra fields, comments or File values.
	if d.offset < uint64(start) {
		return countDirectory(data, int(d.offset), maxEntries)
	}
	return nil
}

func countDirectory(data []byte, offset, maxEntries int) error {
	for count := 0; offset <= len(data)-46 && binary.LittleEndian.Uint32(data[offset:]) == 0x02014b50; count++ {
		if count == maxEntries {
			return errTooManyEntries
		}
		header := data[offset : offset+46]
		size := int(binary.LittleEndian.Uint16(header[28:])) + int(binary.LittleEndian.Uint16(header[30:])) + int(binary.LittleEndian.Uint16(header[32:]))
		if size > len(data)-offset-46 {
			break // zip.NewReader will report the malformed header.
		}
		offset += 46 + size
	}
	return nil
}

func findDirectory(data []byte) (directoryInfo, error) {
	for end := len(data) - 22; end >= max(0, len(data)-65*1024); end-- {
		if binary.LittleEndian.Uint32(data[end:]) != 0x06054b50 {
			continue
		}
		header := data[end : end+22]
		if int(binary.LittleEndian.Uint16(header[20:])) > len(data)-end-22 {
			return directoryInfo{}, zip.ErrFormat
		}
		d := directoryInfo{end: end, records: uint64(binary.LittleEndian.Uint16(header[10:])),
			size: uint64(binary.LittleEndian.Uint32(header[12:])), offset: uint64(binary.LittleEndian.Uint32(header[16:]))}
		// Match archive/zip's ZIP64 locator selection. Ordinary directories may
		// also use a truncated 16-bit count without a ZIP64 locator.
		if d.records == 0xffff || d.size == 0xffff || d.offset == 0xffffffff {
			locator := end - 20
			if locator >= 0 && binary.LittleEndian.Uint32(data[locator:]) == 0x07064b50 &&
				binary.LittleEndian.Uint32(data[locator+4:]) == 0 && binary.LittleEndian.Uint32(data[locator+16:]) == 1 {
				offset := binary.LittleEndian.Uint64(data[locator+8:])
				if len(data) < 56 || offset > uint64(len(data)-56) {
					return directoryInfo{}, zip.ErrFormat
				}
				z := data[int(offset) : int(offset)+56]
				if binary.LittleEndian.Uint32(z) != 0x06064b50 {
					return directoryInfo{}, zip.ErrFormat
				}
				d.end = int(offset)
				d.records = binary.LittleEndian.Uint64(z[32:])
				d.size = binary.LittleEndian.Uint64(z[40:])
				d.offset = binary.LittleEndian.Uint64(z[48:])
			}
		}
		return d, nil
	}
	return directoryInfo{}, zip.ErrFormat
}
