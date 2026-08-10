package whatsmeow

import (
	"context"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
)

type blockingMessageNameStore struct {
	store.NoopStore
	entered chan struct{}
	release chan struct{}
}

func TestMessageNameUpdatesRemainAsyncByDefault(t *testing.T) {
	contacts := &blockingMessageNameStore{entered: make(chan struct{}), release: make(chan struct{})}
	client := &Client{Store: &store.Device{Contacts: contacts}}
	returned := make(chan struct{})
	go func() {
		client.updateMessageContactNames(context.Background(), &types.MessageInfo{
			MessageSource: types.MessageSource{Sender: types.NewJID("15550001111", types.DefaultUserServer)},
			PushName:      "Benchmark Sender",
		})
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("default message name update blocked on the store write")
	}
	<-contacts.entered
	close(contacts.release)
}

func (s *blockingMessageNameStore) PutPushName(context.Context, types.JID, string) (bool, string, error) {
	close(s.entered)
	<-s.release
	return false, "", nil
}

func TestSynchronousMessageNameUpdatesWaitForStore(t *testing.T) {
	contacts := &blockingMessageNameStore{entered: make(chan struct{}), release: make(chan struct{})}
	client := &Client{Store: &store.Device{Contacts: contacts}}
	client.setSynchronousMessageNameUpdates(true)
	done := make(chan struct{})
	go func() {
		client.updateMessageContactNames(context.Background(), &types.MessageInfo{
			MessageSource: types.MessageSource{Sender: types.NewJID("15550001111", types.DefaultUserServer)},
			PushName:      "Benchmark Sender",
		})
		close(done)
	}()

	<-contacts.entered
	select {
	case <-done:
		t.Fatal("synchronous message name update returned before the store write")
	default:
	}
	close(contacts.release)
	<-done
}
