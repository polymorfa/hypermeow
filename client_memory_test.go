package whatsmeow

import (
	"context"
	"net/http"
	"runtime"
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/store"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

func TestNewClientDefersSparseState(t *testing.T) {
	client := NewClient(&store.Device{}, waLog.Noop)
	if client.messageRetries != nil || client.incomingRetryRequestCounter != nil || client.appStateKeyRequests != nil {
		t.Fatal("retry or app-state maps allocated before use")
	}
	if client.groupCache != nil || client.userDevicesCache != nil || client.sessionRecreateHistory != nil {
		t.Fatal("routing caches allocated before use")
	}
	if client.pendingPhoneRerequests != nil || client.responseWaiters != nil || client.tcTokenSenderTS != nil {
		t.Fatal("request maps allocated before use")
	}
	if cap(client.handlerQueue) != handlerQueueSize {
		t.Fatalf("unexpected handler queue capacity: %d", cap(client.handlerQueue))
	}
	if client.mediaHTTP != client.websocketHTTP || client.websocketHTTP != client.preLoginHTTP {
		t.Fatal("default HTTP clients do not share immutable configuration")
	}
	if client.mediaHTTP.Transport != sharedHTTPTransport || client.websocketHTTP.Transport != sharedHTTPTransport || client.preLoginHTTP.Transport != sharedHTTPTransport {
		t.Fatal("default HTTP clients do not share the transport")
	}
}

func TestHandlerQueueOverflowForcesReconnect(t *testing.T) {
	client := NewClient(&store.Device{}, waLog.Noop)
	for range cap(client.handlerQueue) {
		client.handlerQueue <- &waBinary.Node{Tag: "message"}
	}
	client.enqueueNode(context.Background(), &waBinary.Node{Tag: "message"})
	if !client.forceAutoReconnect.Load() {
		t.Fatal("handler queue overflow did not force a reconnect")
	}
}

func TestSetMediaHTTPClientDoesNotChangeOtherClients(t *testing.T) {
	client := NewClient(&store.Device{}, waLog.Noop)
	websocketClient := client.websocketHTTP
	mediaClient := &http.Client{Timeout: 42}
	client.SetMediaHTTPClient(mediaClient)
	if client.mediaHTTP != mediaClient {
		t.Fatal("media HTTP client was not replaced")
	}
	if client.websocketHTTP != websocketClient || client.preLoginHTTP != websocketClient {
		t.Fatal("replacing media HTTP client changed websocket clients")
	}
}

func TestSetProxyClonesSharedHTTPClients(t *testing.T) {
	client := NewClient(&store.Device{}, waLog.Noop)
	defaultClient := client.mediaHTTP
	client.SetProxy(nil, SetProxyOptions{NoMedia: true})
	if client.mediaHTTP != defaultClient {
		t.Fatal("NoMedia proxy option replaced the media client")
	}
	if client.websocketHTTP == defaultClient || client.preLoginHTTP == defaultClient {
		t.Fatal("proxy transport mutated the shared default client")
	}
	if defaultClient.Transport != sharedHTTPTransport {
		t.Fatal("proxy transport mutated shared default transport")
	}
}

func BenchmarkNewClient(b *testing.B) {
	device := &store.Device{}
	clients := make([]*Client, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		clients = append(clients, NewClient(device, waLog.Noop))
	}
	runtime.KeepAlive(clients)
}
