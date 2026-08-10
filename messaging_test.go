package whatsmeow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	waE2E "github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/proto/waWa6"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
)

func TestIQErrorIsDistinguishesSensitiveAttributes(t *testing.T) {
	first := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"token": "first"}}}
	second := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"token": "second"}}}
	if errors.Is(first, second) {
		t.Fatal("errors with distinct sensitive attributes compare equal")
	}
}

func TestIQErrorIsNormalizesEquivalentAttributes(t *testing.T) {
	decoded := &IQError{ErrorNode: &waBinary.Node{Tag: "error"}}
	handBuilt := &IQError{ErrorNode: &waBinary.Node{
		Tag:   "error",
		Attrs: waBinary.Attrs{"ignored-empty": "", "ignored-nil": nil},
		Content: []waBinary.Node{{
			Tag:   "detail",
			Attrs: waBinary.Attrs{},
		}},
	}}
	decoded.ErrorNode.Content = []waBinary.Node{{Tag: "detail"}}
	if !errors.Is(decoded, handBuilt) || !errors.Is(handBuilt, decoded) {
		t.Fatal("semantically equivalent IQ error nodes did not compare equal")
	}
}

func TestIQErrorIsNormalizesEmptyChildLists(t *testing.T) {
	decoded := &IQError{ErrorNode: &waBinary.Node{Tag: "error"}}
	handBuilt := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Content: []waBinary.Node{}}}
	if !errors.Is(decoded, handBuilt) || !errors.Is(handBuilt, decoded) {
		t.Fatal("empty and nil IQ error child lists did not compare equal")
	}
}

func TestIQErrorIsNormalizesEncodedAttributeScalars(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   any
		encoded string
	}{
		{name: "int", value: int(-30), encoded: "-30"},
		{name: "int32", value: int32(-31), encoded: "-31"},
		{name: "int64", value: int64(-32), encoded: "-32"},
		{name: "uint", value: uint(30), encoded: "30"},
		{name: "uint32", value: uint32(31), encoded: "31"},
		{name: "uint64", value: uint64(32), encoded: "32"},
		{name: "bool", value: false, encoded: "false"},
		{name: "bytes", value: []byte("opaque"), encoded: "opaque"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"value": test.encoded}}}
			handBuilt := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"value": test.value}}}
			if !errors.Is(decoded, handBuilt) || !errors.Is(handBuilt, decoded) {
				t.Fatal("wire-equivalent IQ error attributes did not compare equal")
			}
		})
	}
}

func TestIQErrorIsNormalizesEncodedContentScalars(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   any
		encoded string
	}{
		{name: "string", value: "details", encoded: "details"},
		{name: "int", value: int(-30), encoded: "-30"},
		{name: "int32", value: int32(-31), encoded: "-31"},
		{name: "int64", value: int64(-32), encoded: "-32"},
		{name: "uint", value: uint(30), encoded: "30"},
		{name: "uint32", value: uint32(31), encoded: "31"},
		{name: "uint64", value: uint64(32), encoded: "32"},
		{name: "bool", value: false, encoded: "false"},
		{name: "bytes", value: []byte("opaque"), encoded: "opaque"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Content: []byte(test.encoded)}}
			handBuilt := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Content: test.value}}
			if !errors.Is(decoded, handBuilt) || !errors.Is(handBuilt, decoded) {
				t.Fatal("wire-equivalent IQ error content did not compare equal")
			}
		})
	}
}

func TestBuildDeleteNewsletterVariablesRejectsNonNewsletterJID(t *testing.T) {
	tests := []types.JID{
		types.EmptyJID,
		types.NewJID("15551234567", types.DefaultUserServer),
		types.NewJID("120363000000000000", types.GroupServer),
		types.NewJID("not-numeric", types.NewsletterServer),
		types.NewJID(strings.Repeat("1", 257), types.NewsletterServer),
	}
	for _, jid := range tests {
		if _, err := buildDeleteNewsletterVariables(jid); err == nil {
			t.Errorf("expected %q to be rejected", jid)
		}
	}
}

func TestBuildDeleteNewsletterVariablesUsesCanonicalJID(t *testing.T) {
	jid := types.NewJID("120363000000000001", types.NewsletterServer)
	got, err := buildDeleteNewsletterVariables(jid)
	if err != nil {
		t.Fatal(err)
	}
	if got.NewsletterID != "120363000000000001@newsletter" {
		t.Fatalf("newsletter_id = %q", got.NewsletterID)
	}
}

func TestDecodeDeleteNewsletterResponseRequiresMatchingDeletedState(t *testing.T) {
	want := types.NewJID("120363000000000001", types.NewsletterServer)
	tests := []struct {
		name string
		raw  string
	}{
		{"missing discriminator", `{"unexpected":{}}`},
		{"null result", `{"xwa2_newsletter_delete_v2":null}`},
		{"wrong id", `{"xwa2_newsletter_delete_v2":{"id":"120363000000000002@newsletter","state":{"type":"DELETED"}}}`},
		{"active state", `{"xwa2_newsletter_delete_v2":{"id":"120363000000000001@newsletter","state":{"type":"ACTIVE"}}}`},
		{"missing state", `{"xwa2_newsletter_delete_v2":{"id":"120363000000000001@newsletter"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := decodeDeleteNewsletterResponse(json.RawMessage(tc.raw), want); err == nil {
				t.Fatal("expected response validation error")
			}
		})
	}
}

func TestDecodeDeleteNewsletterResponseAcceptsMatchingDeletedState(t *testing.T) {
	want := types.NewJID("120363000000000001", types.NewsletterServer)
	raw := json.RawMessage(`{"xwa2_newsletter_delete_v2":{"id":"120363000000000001@newsletter","state":{"type":"DELETED"}}}`)
	if err := decodeDeleteNewsletterResponse(raw, want); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteNewsletterQueryIsRejectedForDesktopPayloads(t *testing.T) {
	originalPayload := store.BaseClientPayload
	store.BaseClientPayload = &waWa6.ClientPayload{
		UserAgent: &waWa6.ClientPayload_UserAgent{},
	}
	t.Cleanup(func() {
		store.BaseClientPayload = originalPayload
	})

	jid := types.NewJID("15551234567", types.DefaultUserServer)
	client := &Client{Store: &store.Device{ID: &jid}}
	if got := convertQueryID(client, "30062808666639665"); got != "" {
		t.Fatalf("desktop query ID = %q, want unsupported", got)
	}
}

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

func TestButtonAndListResponsesDoNotRequestBusinessMetadata(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.Message
	}{
		{name: "buttons response", msg: &waE2E.Message{ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{}}},
		{name: "list response", msg: &waE2E.Message{ListResponseMessage: &waE2E.ListResponseMessage{}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := getButtonTypeFromMessage(tc.msg); got != "" {
				t.Fatalf("response requested unexpected business metadata type %q", got)
			}
		})
	}
}

func TestInteractiveNativeFlowsRequestNamedBusinessMetadata(t *testing.T) {
	for _, name := range []string{"address_message", "galaxy_message"} {
		t.Run(name, func(t *testing.T) {
			msg := &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
				InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
					Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{{Name: proto.String(name)}},
				}},
			}}
			if got := getButtonTypeFromMessage(msg); got != "native_flow" {
				t.Fatalf("button type = %q", got)
			}
			biz := buildNativeFlowBizNode(msg, 1_700_000_000)
			if biz.Tag != "biz" || biz.Attrs["actual_actors"] != "2" || biz.Attrs["host_storage"] != "2" || biz.Attrs["privacy_mode_ts"] != "1700000000" {
				t.Fatalf("unexpected biz attrs: %#v", biz)
			}
			children, ok := biz.Content.([]waBinary.Node)
			if !ok || len(children) != 2 || children[0].Tag != "interactive" {
				t.Fatalf("unexpected biz children: %#v", biz.Content)
			}
			flowChildren := children[0].Content.([]waBinary.Node)
			if flowChildren[0].Tag != "native_flow" || flowChildren[0].Attrs["name"] != name || flowChildren[0].Attrs["v"] != "9" {
				t.Fatalf("unexpected native-flow metadata: %#v", flowChildren[0])
			}
		})
	}
}

func TestHeterogeneousNativeFlowsRequestMixedBusinessMetadata(t *testing.T) {
	msg := &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
			Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
				{Name: proto.String("quick_reply")},
				{Name: proto.String("cta_url")},
			},
		}},
	}}
	biz := buildNativeFlowBizNode(msg, 1_700_000_000)
	flow := biz.Content.([]waBinary.Node)[0].Content.([]waBinary.Node)[0]
	if flow.Attrs["name"] != "mixed" {
		t.Fatalf("native-flow name = %q", flow.Attrs["name"])
	}
}

func TestNativeFlowBusinessMetadataUnwrapsMessages(t *testing.T) {
	inner := &waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
			Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{{Name: proto.String("galaxy_message")}},
		}},
	}}
	wrappers := map[string]*waE2E.Message{
		"view once":              {ViewOnceMessage: &waE2E.FutureProofMessage{Message: inner}},
		"view once v2":           {ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: inner}},
		"view once v2 extension": {ViewOnceMessageV2Extension: &waE2E.FutureProofMessage{Message: inner}},
		"ephemeral":              {EphemeralMessage: &waE2E.FutureProofMessage{Message: inner}},
	}
	for name, message := range wrappers {
		t.Run(name, func(t *testing.T) {
			if got := getButtonTypeFromMessage(message); got != "native_flow" {
				t.Fatalf("button type = %q", got)
			}
			biz := buildNativeFlowBizNode(message, 1_700_000_000)
			flow := biz.Content.([]waBinary.Node)[0].Content.([]waBinary.Node)[0]
			if flow.Attrs["name"] != "galaxy_message" {
				t.Fatalf("native-flow name = %q", flow.Attrs["name"])
			}
		})
	}
}

func TestListBusinessMetadataUnwrapsViewOnceV2Extension(t *testing.T) {
	msg := &waE2E.Message{ViewOnceMessageV2Extension: &waE2E.FutureProofMessage{Message: &waE2E.Message{
		ListMessage: &waE2E.ListMessage{ListType: waE2E.ListMessage_SINGLE_SELECT.Enum()},
	}}}
	attrs := getButtonAttributes(msg)
	if attrs["v"] != "2" || attrs["type"] != "single_select" {
		t.Fatalf("unexpected list metadata: %#v", attrs)
	}
}

func TestSetParticipantHashMismatch(t *testing.T) {
	tests := []struct {
		name string
		sent string
		ack  string
		want bool
	}{
		{name: "matching", sent: "same", ack: "same"},
		{name: "missing acknowledgement hash", sent: "sent"},
		{name: "mismatch", sent: "old", ack: "new", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := SendResponse{}
			if got := setParticipantHashMismatch(&resp, tc.sent, tc.ack); got != tc.want {
				t.Fatalf("mismatch = %t, want %t", got, tc.want)
			}
			if resp.PHashMismatch != tc.want {
				t.Fatalf("response mismatch = %t, want %t", resp.PHashMismatch, tc.want)
			}
		})
	}
}

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
