// Copyright (c) 2026 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Both hooks take their argument by value rather than by pointer, and these
// exist to keep it that way. A pointer would escape through the indirect call
// and cost one heap allocation per stanza. Small, and on the receive path of
// a long-running process, which is where small and per-stanza adds up.
//
// Compare the two: the numbers have to match.
//
//	go test -run XXX -bench BenchmarkRawNodeHook -benchmem
func benchmarkFrame(b *testing.B) []byte {
	b.Helper()
	frame, err := waBinary.Marshal(waBinary.Node{
		Tag:   "receipt",
		Attrs: waBinary.Attrs{"id": "ABCD1234", "type": "read"},
	})
	if err != nil {
		b.Fatalf("failed to marshal fixture: %v", err)
	}
	return frame
}

func BenchmarkRawNodeHookUnset(b *testing.B) {
	cli := NewClient(&store.Device{}, waLog.Noop)
	// Drained, so the queue depth does not become the thing being measured.
	go func() {
		for range cli.handlerQueue {
		}
	}()
	frame := benchmarkFrame(b)

	b.ReportAllocs()
	for b.Loop() {
		cli.handleFrame(context.Background(), frame)
	}
}

func BenchmarkRawNodeHookSet(b *testing.B) {
	cli := NewClient(&store.Device{}, waLog.Noop)
	cli.RawNodeHandler = func(_ context.Context, raw RawNode) (*waBinary.Node, bool) {
		if len(raw.Frame) == 0 {
			b.Fatal("the hook was handed no frame")
		}
		return nil, true
	}
	frame := benchmarkFrame(b)

	b.ReportAllocs()
	for b.Loop() {
		cli.handleFrame(context.Background(), frame)
	}
}
