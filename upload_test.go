package whatsmeow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type failingReadSeeker struct{}

func (failingReadSeeker) Read([]byte) (int, error) {
	return 0, errors.New("fixture read failure")
}

func (failingReadSeeker) Seek(int64, int) (int64, error) {
	return 0, nil
}

func TestUploadNewsletterReaderReturnsHashingError(t *testing.T) {
	client := &Client{}
	_, err := client.UploadNewsletterReader(context.Background(), failingReadSeeker{}, MediaImage)
	if err == nil || !strings.Contains(err.Error(), "failed to hash newsletter upload") {
		t.Fatalf("UploadNewsletterReader error = %v", err)
	}
}

var _ io.ReadSeeker = failingReadSeeker{}
