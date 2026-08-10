package whatsmeow

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/store"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

func TestPutBoundedCache(t *testing.T) {
	cache := make(map[string]int, 2)
	putBoundedCache(cache, "one", 1, 2)
	putBoundedCache(cache, "two", 2, 2)
	putBoundedCache(cache, "three", 3, 2)

	if len(cache) != 2 {
		t.Fatalf("cache grew to %d entries", len(cache))
	}
	if cache["three"] != 3 {
		t.Fatal("new value was not cached")
	}
	putBoundedCache(cache, "three", 4, 2)
	if len(cache) != 2 || cache["three"] != 4 {
		t.Fatal("updating a value changed the cache size")
	}
}

func TestPruneExpiredCache(t *testing.T) {
	now := time.Now()
	cache := map[string]time.Time{
		"old": now.Add(-2 * time.Hour),
		"new": now,
	}
	pruneExpiredCache(cache, now.Add(-time.Hour))

	if _, ok := cache["old"]; ok {
		t.Fatal("expired entry was retained")
	}
	if _, ok := cache["new"]; !ok {
		t.Fatal("fresh entry was removed")
	}
}

func TestClientCachesStayBounded(t *testing.T) {
	messageRetries := make(map[string]int, maxMessageRetryEntries)
	for i := 0; i <= maxMessageRetryEntries; i++ {
		key := fmt.Sprintf("message-%d", i)
		putBoundedCache(messageRetries, key, i, maxMessageRetryEntries)
	}
	if len(messageRetries) != maxMessageRetryEntries {
		t.Fatalf("message retry cache grew to %d entries", len(messageRetries))
	}
}

func TestIncrementBoundedCounterFailsClosedAndResets(t *testing.T) {
	now := time.Now()
	resetAt := now
	cache := map[string]int{"one": 1}
	if _, accepted := incrementBoundedCounter(cache, "two", 1, &resetAt, now); accepted {
		t.Fatal("accepted a new counter after reaching capacity")
	}
	count, accepted := incrementBoundedCounter(cache, "two", 1, &resetAt, now.Add(retryCounterWindow))
	if !accepted || count != 1 {
		t.Fatalf("counter did not reset: accepted=%t count=%d", accepted, count)
	}
	if _, exists := cache["one"]; exists {
		t.Fatal("old counter survived reset")
	}
}

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

func TestRefreshMediaConnConcurrentCacheReads(t *testing.T) {
	cached := &MediaConn{TTL: 60, FetchedAt: time.Now(), Hosts: []MediaConnHost{{Hostname: "media.example.test"}}}
	client := &Client{mediaConnCache: cached}

	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, err := client.refreshMediaConn(context.Background(), false)
			if err != nil {
				t.Errorf("refreshMediaConn failed: %v", err)
			} else if got != cached {
				t.Errorf("refreshMediaConn returned %p, want %p", got, cached)
			}
		}()
	}
	wait.Wait()
}
