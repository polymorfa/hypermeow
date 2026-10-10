package whatsmeow

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
)

func abpropsNotification() *waBinary.Node {
	return &waBinary.Node{Tag: "notification", Attrs: waBinary.Attrs{"type": "server", "id": "1", "from": types.ServerJID}, Content: []waBinary.Node{{Tag: "abprops"}}}
}
func TestABPropsAckWaitsWithoutBlockingReceiveLoop(t *testing.T) {
	started := make(chan struct{})
	complete := make(chan struct{})
	acked := make(chan struct{}, 1)
	var tasks atomic.Int32
	cli := &Client{ServerABPropsSync: func(context.Context) error { tasks.Add(1); close(started); <-complete; return nil }}
	node := abpropsNotification()
	if !cli.deferServerABProps(context.Background(), node, func() { acked <- struct{}{} }) {
		t.Fatal("not deferred")
	}
	<-started
	if !cli.deferServerABProps(context.Background(), node, func() { t.Error("duplicate ack") }) {
		t.Fatal("duplicate not coalesced")
	}
	select {
	case <-acked:
		t.Fatal("acked before task/IQ completion")
	default:
	}
	close(complete)
	select {
	case <-acked:
	case <-time.After(time.Second):
		t.Fatal("missing ack")
	}
	if tasks.Load() != 1 {
		t.Fatal("duplicate task")
	}
}
func TestABPropsAbsentUnrelatedFailedAndCancelled(t *testing.T) {
	if (&Client{}).deferServerABProps(context.Background(), abpropsNotification(), nil) {
		t.Fatal("nil handler changed path")
	}
	for _, failure := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if !failure {
			cancel()
		}
		done := make(chan struct{})
		cli := &Client{ServerABPropsSync: func(context.Context) error { defer close(done); return errors.New("incomplete") }}
		if !cli.deferServerABProps(ctx, abpropsNotification(), func() { t.Error("failed/cancelled task acked") }) {
			t.Fatal("not deferred")
		}
		if failure {
			<-done
		}
		cancel()
	}
	cli := &Client{ServerABPropsSync: func(context.Context) error { t.Fatal("unrelated callback"); return nil }}
	node := abpropsNotification()
	node.Attrs["type"] = "encrypt"
	if cli.deferServerABProps(context.Background(), node, nil) {
		t.Fatal("unrelated deferred")
	}
	node = abpropsNotification()
	node.Content = nil
	if cli.deferServerABProps(context.Background(), node, nil) {
		t.Fatal("missing child deferred")
	}
}

func TestABPropsSyncHasBoundedContextAndReleasesCancelledTask(t *testing.T) {
	started := make(chan time.Time, 1)
	finished := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	cli := &Client{ServerABPropsSync: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("unbounded callback")
		}
		started <- deadline
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	}}
	cli.deferServerABProps(ctx, abpropsNotification(), func() { t.Error("cancelled task acknowledged") })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("callback not started")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("callback not cancelled")
	}
	deadline := time.Now().Add(time.Second)
	for {
		cli.serverABPropsMu.Lock()
		pending := len(cli.serverABPropsPending)
		cli.serverABPropsMu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled task retained its slot")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestABPropsBackgroundTaskReceivesDeadline(t *testing.T) {
	done := make(chan struct{})
	cli := &Client{ServerABPropsSync: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Minute || time.Until(deadline) <= 0 {
			t.Error("invalid task deadline")
		}
		close(done)
		return errors.New("incomplete")
	}}
	cli.deferServerABProps(context.Background(), abpropsNotification(), func() { t.Error("failed task acknowledged") })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("callback not started")
	}
}

func TestABPropsSaturationWithholdsWithoutStartingAnotherTask(t *testing.T) {
	cli := &Client{ServerABPropsSync: func(context.Context) error { t.Error("saturated callback started"); return nil }, serverABPropsPending: make(map[string]struct{})}
	for i := 0; i < 256; i++ {
		cli.serverABPropsPending[string(rune(i))] = struct{}{}
	}
	if !cli.deferServerABProps(context.Background(), abpropsNotification(), func() { t.Error("saturated notification acknowledged") }) {
		t.Fatal("notification fell through to ordinary ack")
	}
	if len(cli.serverABPropsPending) != 256 {
		t.Fatal("pending bound changed")
	}
}
