# Optional native send admission

`SendRequestExtra.NativeSend` is process-local and nil by default. When it is nil, the send path
is unchanged. It supports one ordinary text DM with a preallocated message ID. Groups, peer
messages, bots, media, meta nodes and additional nodes are refused before preparation. The hook
is not a renderer and grants nothing.

## Where it runs

The hook runs in `sendDM` at the message-node handoff, after every preparation await (device
lookup, session setup, encryption, privacy token). It replaces only the guarded call to
`sendNodeAndGetData`. Nothing in `socket/` changes, and no socket or Noise lock is held while
`Admit` runs. Other traffic on the connection (receipts, presence, pings, IQs) is never blocked
by admission. `SendMessage` already holds `messageSendLock` here, so a slow `Admit` can delay
only other message sends on the same client, and `NativeAdmitTimeout` bounds that delay.

## Contract

- `Admit(ctx, id, write)` must call `write` at most once, synchronously. Returning without
  calling it refuses the send (`ErrNativeSendRefused`), and nothing is written.
- `write` closes when `Admit` returns. A retained `write` called later is refused. If `write` is
  running when `Admit` returns, the wrapper waits for it, so no write can start or still be
  running after the send call returns.
- `write(sendCtx)` calls `Entered()` and then hands the node to the existing sender with
  `sendCtx`. Cancel `sendCtx` when authority is revoked. The Noise writer already checks the
  context after taking its write lock and before encrypting, so a revocation while the frame
  waits in the writer queue refuses the write without consuming a nonce.
- `Entered()` means the node was handed to the sender. Any failure after it returns
  `ErrNativeSendUncertain`: the message may have been delivered and must never be resent.

## Retries

Guarded sends do not populate the retry message cache and do not use the disconnect
`retryFrame` resend. A disconnect response returns `ErrNativeSendUncertain`. Integrators must
keep their `GetMessageForRetry` fallback from returning guarded content and must not wrap this
API in an automatic retry. Durable retry, receipt and outbox policy belongs to the integrating
application.
