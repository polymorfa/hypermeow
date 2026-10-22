package whatsmeow

import (
	"context"
	"runtime/debug"

	"github.com/polymorfa/hypermeow/appstate"
)

// Parameters copy the original direct go-call arguments before scheduling.
// Admission refusal must not run any key lookup, request-cache or send body.
func (cli *Client) startMissingAppStateKeyRequest(ctx context.Context, patches *appstate.PatchList) bool {
	return cli.startConnectionTask(ctx, func(ctx context.Context) { cli.requestMissingAppStateKeys(ctx, patches) })
}

// The closure keeps the original event-slice capture and member order. A single
// admitted batch owns its synchronous dispatches through their actual return,
// including members reached after sealing. This dispatcher must not escape to
// independently spawned work. It grants no admission to user-spawned children.
func (cli *Client) startAppStateEventBatch(batch func(dispatch func(any) bool)) bool {
	return cli.startNative(func() { batch(cli.dispatchAdmittedAppStateEvent) })
}

// Match dispatchEvent's synchronous handler-list, failure and panic semantics.
// The enclosing batch already owns admission; rechecking the seal per member or
// handler would truncate an admitted batch. The general dispatcher is unchanged.
func (cli *Client) dispatchAdmittedAppStateEvent(evt any) (handlerFailed bool) {
	cli.eventHandlersLock.RLock()
	defer func() {
		cli.eventHandlersLock.RUnlock()
		err := recover()
		if err != nil {
			cli.Log.Errorf("Event handler panicked while handling a %T: %v\n%s", evt, err, debug.Stack())
		}
	}()
	for _, handler := range cli.eventHandlers {
		success := handler.fn(evt)
		if !success {
			return true
		}
	}
	return false
}
