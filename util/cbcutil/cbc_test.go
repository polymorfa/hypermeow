package cbcutil

import (
	"errors"
	"io"
	"testing"
)

type shortWriter struct {
	data []byte
}

func (writer *shortWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	written := min(3, len(data))
	writer.data = append(writer.data, data[:written]...)
	return written, nil
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) {
	return 0, nil
}

func TestWriteAllHandlesShortWrites(t *testing.T) {
	writer := &shortWriter{}
	want := []byte("a payload larger than one short write")
	if err := writeAll(writer, want); err != nil {
		t.Fatalf("writeAll failed: %v", err)
	}
	if string(writer.data) != string(want) {
		t.Fatalf("writeAll wrote %q, want %q", writer.data, want)
	}
}

func TestWriteAllRejectsNoProgress(t *testing.T) {
	if err := writeAll(zeroWriter{}, []byte("payload")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeAll error = %v, want %v", err, io.ErrShortWrite)
	}
}
