package whatsmeow

import (
	"context"
	"sync"
	"testing"
	"time"
)

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
