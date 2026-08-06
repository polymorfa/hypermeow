package whatsmeow

import (
	"fmt"
	"testing"
	"time"
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
