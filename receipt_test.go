package whatsmeow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/types"
)

func TestMarkReadRejectsMultipleReceiptTypes(t *testing.T) {
	client := &Client{}
	err := client.MarkRead(
		context.Background(),
		[]types.MessageID{"message"},
		time.Now(),
		types.NewJID("123", types.DefaultUserServer),
		types.EmptyJID,
		types.ReceiptTypePlayed,
		types.ReceiptTypeRead,
	)
	if err == nil || !strings.Contains(err.Error(), "too many receipt types") {
		t.Fatalf("MarkRead error = %v", err)
	}
}
