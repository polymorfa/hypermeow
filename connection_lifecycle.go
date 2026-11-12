// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"sync/atomic"

	"github.com/polymorfa/hypermeow/types/events"
)

// ConnectionEventHandler receives Connected, Disconnected, QR, LoggedOut,
// StreamReplaced and CATRefreshError with the original connection's ConnectContext.
// The context may
// already be cancelled when a delayed event arrives. Consumers must check their
// own current connection authority; an event does not establish ownership.
// Other events continue through the existing generic event API only.
type ConnectionEventHandler func(context.Context, any)

type wrappedConnectionEventHandler struct {
	fn ConnectionEventHandler
	id uint32
}

// AddConnectionEventHandler adds a lifecycle observer without replacing generic
// event handlers. A handler may reenter this registry without holding its lock.
func (cli *Client) AddConnectionEventHandler(handler ConnectionEventHandler) uint32 {
	id := atomic.AddUint32(&nextHandlerID, 1)
	cli.connectionEventHandlersLock.Lock()
	cli.connectionEventHandlers = append(cli.connectionEventHandlers, wrappedConnectionEventHandler{handler, id})
	cli.connectionEventHandlersLock.Unlock()
	return id
}

// RemoveConnectionEventHandler removes a lifecycle observer. A callback already
// captured by an in-flight dispatch can still arrive with its original context.
func (cli *Client) RemoveConnectionEventHandler(id uint32) bool {
	cli.connectionEventHandlersLock.Lock()
	defer cli.connectionEventHandlersLock.Unlock()
	for i, handler := range cli.connectionEventHandlers {
		if handler.id == id {
			copy(cli.connectionEventHandlers[i:], cli.connectionEventHandlers[i+1:])
			cli.connectionEventHandlers[len(cli.connectionEventHandlers)-1] = wrappedConnectionEventHandler{}
			cli.connectionEventHandlers = cli.connectionEventHandlers[:len(cli.connectionEventHandlers)-1]
			return true
		}
	}
	return false
}

// ctx is supplied by the original connection producer before any asynchronous
// scheduling. Never replace it with client.BackgroundEventCtx or current state.
func (cli *Client) dispatchConnectionEvent(ctx context.Context, evt any) (handlerFailed bool) {
	switch evt.(type) {
	case *events.Connected, *events.Disconnected, *events.QR, *events.LoggedOut, *events.StreamReplaced, *events.CATRefreshError:
		cli.connectionEventHandlersLock.Lock()
		handlers := append([]wrappedConnectionEventHandler(nil), cli.connectionEventHandlers...)
		cli.connectionEventHandlersLock.Unlock()
		for _, handler := range handlers {
			invokeConnectionEventHandler(cli, handler.fn, ctx, evt)
		}
	}
	return cli.dispatchEvent(evt)
}

func invokeConnectionEventHandler(cli *Client, handler ConnectionEventHandler, ctx context.Context, evt any) {
	defer func() {
		if recover() != nil {
			cli.Log.Errorf("Connection lifecycle event handler panicked")
		}
	}()
	handler(ctx, evt)
}
