package whatsmeow

import (
	"slices"
	"sync"
	"testing"
	"time"
)

func TestConcurrentUnifiedSessionChecksRespectInterval(t *testing.T) {
	cli, _, sent := newTestRemote(t)
	cli.UnifiedSessionInterval = time.Hour
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			<-start
			cli.sendUnifiedSession(true)
		})
	}
	close(start)
	workers.Wait()
	if tags := sent.tags(); !slices.Equal(tags, []string{"ib:"}) {
		t.Fatalf("concurrent interval checks sent %v", tags)
	}
	cli.sendUnifiedSession(false)
	if len(sent.tags()) != 2 {
		t.Fatal("explicit unified-session send was incorrectly suppressed")
	}
}
