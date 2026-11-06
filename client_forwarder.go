package whatsmeow

import (
	"context"
	"sync"
)

// A registration owns completion separately from active tasks and pinned scopes.
// Counting it as active would cycle with pins released only after the active join.
type nativeForwarder struct {
	done chan struct{}
	once sync.Once
}

// registerForwarder is called while nativeContext's constructor has a reserved
// active owner. Publish the exact entry before AfterFunc can start, including
// when an admitted constructor reaches an already-canceled lifetime.
func (l *clientLifetime) registerForwarder(parent context.Context, cancel context.CancelFunc) func() bool {
	return l.registerForwarderUsing(parent, cancel, context.AfterFunc)
}

// The registrar argument permits a deterministic test of callback completion
// before its stop function is published. Production always uses context.AfterFunc.
func (l *clientLifetime) registerForwarderUsing(parent context.Context, cancel context.CancelFunc, after func(context.Context, func()) func() bool) func() bool {
	entry := &nativeForwarder{done: make(chan struct{})}
	l.mu.Lock()
	if l.forwarders == nil {
		l.forwarders = make(map[*nativeForwarder]struct{})
	}
	l.forwarders[entry] = struct{}{}
	l.mu.Unlock()
	stop := after(parent, func() {
		defer l.completeForwarder(entry)
		cancel()
	})
	// Callback completion does not depend on publication of this stop function.
	// Go's stop contract excludes a callback start when stop returns true; false
	// does not join a callback that has already started (or already completed).
	return func() bool {
		if stop() {
			l.completeForwarder(entry)
			return true
		}
		return false
	}
}

func (l *clientLifetime) completeForwarder(entry *nativeForwarder) {
	entry.once.Do(func() {
		l.mu.Lock()
		delete(l.forwarders, entry)
		close(entry.done)
		l.mu.Unlock()
	})
}

// Only finalDisconnect drains: after active owners join and pins release, every
// production nativeContext constructor has returned, so no new registration can
// appear. Ordinary last-reference cleanup retains stop(); cancel() without an
// added join. A custom Context can still block inside its own cancel cleanup.
func (l *clientLifetime) waitForwarders() {
	l.mu.Lock()
	pending := make([]<-chan struct{}, 0, len(l.forwarders))
	for entry := range l.forwarders {
		pending = append(pending, entry.done)
	}
	l.mu.Unlock()
	for _, done := range pending {
		<-done
	}
}
