// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types/events"
)

func TestConnectionLifecycleRetainsOriginalContextAfterDelayedDispatch(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	type sourceKey struct{}
	original, cancel := context.WithCancel(context.WithValue(context.Background(), sourceKey{}, "old"))
	defer cancel()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	generic := atomic.Int32{}
	cli.AddEventHandler(func(evt any) {
		if _, ok := evt.(*events.StreamReplaced); ok {
			generic.Add(1)
			close(done)
		}
	})
	var got context.Context
	id := cli.AddConnectionEventHandler(func(ctx context.Context, evt any) {
		if _, ok := evt.(*events.StreamReplaced); !ok {
			t.Error("wrong event")
		}
		got = ctx
		close(entered)
		<-release
	})
	cli.handleStreamError(original, &waBinary.Node{Tag: "stream:error", Content: []waBinary.Node{{Tag: "conflict", Attrs: waBinary.Attrs{"type": "replaced"}}}})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("actual lifecycle producer did not dispatch")
	}
	cancel()
	cli.BackgroundEventCtx = context.WithValue(context.Background(), sourceKey{}, "new")
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("generic callback lost")
	}
	if got != original || got.Value(sourceKey{}) != "old" || got.Err() != context.Canceled {
		t.Fatal("late lifecycle event relabelled with current context")
	}
	if generic.Load() != 1 {
		t.Fatal("generic consumers changed")
	}
	if !cli.RemoveConnectionEventHandler(id) || cli.RemoveConnectionEventHandler(id) {
		t.Fatal("scoped callback removal failed")
	}
}

func TestConnectionCallbacksCanReenterRegistryAndGenericDeliveryRemains(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	var scoped, generic int
	var id uint32
	id = cli.AddConnectionEventHandler(func(context.Context, any) { scoped++; cli.RemoveConnectionEventHandler(id) })
	cli.AddEventHandler(func(any) { generic++ })
	cli.dispatchConnectionEvent(context.Background(), &events.Connected{})
	cli.dispatchConnectionEvent(context.Background(), &events.Connected{})
	if scoped != 1 || generic != 2 {
		t.Fatal("scoped handler reentry or generic delivery failed")
	}
}
