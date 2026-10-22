package whatsmeow

import (
	"context"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	"github.com/rs/zerolog"
)

// Typed parameters retain the original go-call argument evaluation before launch.
func (cli *Client) startMessageNameUpdate(ctx context.Context, user, alt types.JID, info *types.MessageInfo, name string, business bool) bool {
	return cli.startConnectionTask(ctx, func(ctx context.Context) {
		if business {
			cli.updateBusinessName(ctx, user, alt, info, name)
		} else {
			cli.updatePushName(ctx, user, alt, info, name)
		}
	})
}
func (cli *Client) startAck(ctx context.Context, node *waBinary.Node, code int) bool {
	return cli.startConnectionTask(ctx, func(ctx context.Context) { cli.sendAck(ctx, node, code) })
}
func (cli *Client) startRetryReceipt(ctx context.Context, node *waBinary.Node, info *types.MessageInfo, force bool) bool {
	return cli.startConnectionTask(ctx, func(ctx context.Context) { cli.sendRetryReceipt(ctx, node, info, force) })
}
func (cli *Client) startRetryHandler(ctx context.Context, receipt *events.Receipt, node *waBinary.Node) bool {
	return cli.startConnectionTask(ctx, func(ctx context.Context) { cli.tryHandleRetryReceipt(ctx, receipt, node) })
}
func (cli *Client) startAppStateKeyShare(ctx context.Context, keys *waE2E.AppStateSyncKeyShare) bool {
	return cli.startConnectionTask(ctx, func(ctx context.Context) { cli.handleAppStateSyncKeyShare(ctx, keys) })
}
func (cli *Client) backgroundIfAsyncAckContext(ctx context.Context, fn func(context.Context)) {
	if cli.SynchronousAck {
		fn(ctx)
	} else {
		cli.startConnectionTask(ctx, fn)
	}
}

func (cli *Client) startEventBufferCleanup(ctx context.Context) bool {
	cli.decryptedBufferClearMu.Lock()
	if cli.decryptedBufferClearStarted || time.Since(cli.lastDecryptedBufferClear) <= 12*time.Hour || ctx.Err() != nil {
		cli.decryptedBufferClearMu.Unlock()
		return false
	}
	cli.decryptedBufferClearStarted = true
	clearAt := time.Now()
	cli.decryptedBufferClearMu.Unlock()
	if !cli.startConnectionTask(context.WithoutCancel(ctx), func(taskCtx context.Context) {
		defer func() {
			cli.decryptedBufferClearMu.Lock()
			cli.decryptedBufferClearStarted = false
			cli.decryptedBufferClearMu.Unlock()
		}()
		cli.decryptedBufferClearMu.Lock()
		cli.lastDecryptedBufferClear = clearAt
		cli.decryptedBufferClearMu.Unlock()
		err := cli.Store.EventBuffer.DeleteOldBufferedHashes(taskCtx)
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to delete old buffered hashes")
		}
	}) {
		cli.decryptedBufferClearMu.Lock()
		cli.decryptedBufferClearStarted = false
		cli.decryptedBufferClearMu.Unlock()
		return false
	}
	return true
}

type historySyncLoopOwner struct{ reserved bool }

func (cli *Client) claimHistorySyncLoop() *historySyncLoopOwner {
	cli.historySyncHandlerMu.Lock()
	defer cli.historySyncHandlerMu.Unlock()
	if cli.historySyncHandlerOwner != nil {
		return nil
	}
	owner := &historySyncLoopOwner{reserved: true}
	cli.historySyncHandlerOwner = owner
	cli.historySyncHandlerStarted.Store(true)
	return owner
}
func (cli *Client) releaseHistorySyncLoop(owner *historySyncLoopOwner) bool {
	cli.historySyncHandlerMu.Lock()
	defer cli.historySyncHandlerMu.Unlock()
	if owner == nil || cli.historySyncHandlerOwner != owner {
		return false
	}
	cli.historySyncHandlerOwner = nil
	cli.historySyncHandlerStarted.Store(false)
	return true
}
func (cli *Client) startHistorySyncLoop(restart bool) bool {
	owner := cli.claimHistorySyncLoop()
	if owner == nil {
		return false
	}
	if !cli.startConnectionTask(cli.BackgroundEventCtx, func(ctx context.Context) { cli.runHistorySyncNotificationLoop(ctx, owner, restart) }) {
		cli.releaseHistorySyncLoop(owner)
		return false
	}
	return true
}

// The synchronous DangerousInternals adapter retains its signature and takes its
// own exact claim; it cannot clear or duplicate a different running loop owner.
func (cli *Client) handleHistorySyncNotificationLoop() {
	owner := cli.claimHistorySyncLoop()
	if owner == nil {
		return
	}
	done, ok := cli.enterNative()
	if !ok {
		cli.releaseHistorySyncLoop(owner)
		return
	}
	defer done()
	ctx, release := cli.nativeContext(cli.BackgroundEventCtx)
	defer release()
	cli.runHistorySyncNotificationLoop(ctx, owner, false)
}
func (cli *Client) enqueueHistorySyncNotification(queued historySyncNotification) bool {
	if cli.nativeClosed() {
		return false
	}
	select {
	case <-cli.nativeDone():
		return false
	case cli.historySyncNotifications <- queued:
	}
	if cli.nativeClosed() {
		return false
	}
	cli.startHistorySyncLoop(false)
	return !cli.nativeClosed()
}

type pendingPhoneRerequest struct{ cancel context.CancelFunc }

func (cli *Client) startDelayedRequestMessageFromPhone(info *types.MessageInfo) bool {
	return cli.startConnectionTask(cli.BackgroundEventCtx, func(ctx context.Context) { cli.delayedRequestMessageFromPhoneContext(ctx, info) })
}

// Caller-owned synchronous adapter keeps the original Background ancestry.
func (cli *Client) delayedRequestMessageFromPhone(info *types.MessageInfo) {
	cli.delayedRequestMessageFromPhoneContext(cli.BackgroundEventCtx, info)
}
