package whatsmeow

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

func TestRecentMessageCacheStoresSerializedMessage(t *testing.T) {
	cli := &Client{}
	to := types.NewJID("123", types.DefaultUserServer)
	message := &waE2E.Message{Conversation: proto.String("original")}
	if err := cli.addRecentMessage(context.Background(), to, "message", message, nil); err != nil {
		t.Fatalf("failed to cache message: %v", err)
	}
	message.Conversation = proto.String("mutated")

	cached := cli.getRecentMessage(to, "message")
	if cached.wa.GetConversation() != "original" {
		t.Fatalf("cached message changed to %q", cached.wa.GetConversation())
	}
	if len(cli.recentMessagesMap[recentMessageKey{To: to, ID: "message"}].payload) == 0 {
		t.Fatal("serialized payload is empty")
	}
}

func TestRecentMessageCacheGrowsOnDemand(t *testing.T) {
	cli := &Client{}
	to := types.NewJID("123", types.DefaultUserServer)
	message := &waE2E.Message{Conversation: proto.String("first")}
	if err := cli.addRecentMessage(context.Background(), to, "message", message, nil); err != nil {
		t.Fatalf("failed to cache message: %v", err)
	}
	if len(cli.recentMessagesList) != 1 {
		t.Fatalf("recent message ring length = %d, want 1", len(cli.recentMessagesList))
	}
}

func TestRecentMessageCacheDoesNotDuplicateKeys(t *testing.T) {
	cli := &Client{}
	to := types.NewJID("123", types.DefaultUserServer)
	for _, text := range []string{"first", "updated"} {
		message := &waE2E.Message{Conversation: proto.String(text)}
		if err := cli.addRecentMessage(context.Background(), to, "message", message, nil); err != nil {
			t.Fatalf("failed to cache message: %v", err)
		}
	}
	if len(cli.recentMessagesList) != 1 {
		t.Fatalf("recent message ring length = %d, want 1", len(cli.recentMessagesList))
	}
	if got := cli.getRecentMessage(to, "message").wa.GetConversation(); got != "updated" {
		t.Fatalf("cached message = %q, want updated", got)
	}
}

func TestRecentMessageCacheEvictsOldest(t *testing.T) {
	cli := &Client{}
	to := types.NewJID("123", types.DefaultUserServer)
	for i := 0; i <= recentMessagesSize; i++ {
		id := fmt.Sprintf("message-%d", i)
		message := &waE2E.Message{Conversation: proto.String(id)}
		if err := cli.addRecentMessage(context.Background(), to, id, message, nil); err != nil {
			t.Fatalf("failed to cache message: %v", err)
		}
	}

	if !cli.getRecentMessage(to, "message-0").IsEmpty() {
		t.Fatal("oldest message was retained")
	}
	if cli.getRecentMessage(to, "message-1").IsEmpty() {
		t.Fatal("second-oldest message was evicted")
	}
	if cli.getRecentMessage(to, fmt.Sprintf("message-%d", recentMessagesSize)).IsEmpty() {
		t.Fatal("newest message was not cached")
	}
}
