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
only other message sends on the same client, and the enforced `NativeAdmitTimeout` bounds
that delay.

## Contract

- `Admit(ctx, id, write)` must call `write` at most once, synchronously. Returning without
  calling it refuses the send (`ErrNativeSendRefused`), and nothing is written.
- `NativeAdmitTimeout` (2 s) is enforced. If `Admit` has not returned by then, the send is
  refused and `write` is closed, even if `Admit` ignores its context. A panic in `Admit` before
  `write` also refuses.
- Call `write` synchronously from `Admit`. `write` closes when the send call stops waiting for
  `Admit` (return or deadline). A `write` that is not synchronized before that point, such as one
  from another goroutine, may either refuse or complete. Either way, no write can start or still
  be running after the send call returns.
- `write(sendCtx)` calls `Entered()` and then hands the node to the existing sender with
  `sendCtx`. Cancel `sendCtx` when authority is revoked.
- `Entered()` means the node was handed to the sender. Any failure after it, including a panic,
  returns `ErrNativeSendUncertain`: the message may have been delivered and must never be
  resent. For guarded sends, `SendMessage` also wraps the acknowledgment timeout, context
  cancellation while waiting, a disconnect response and a server error code in
  `ErrNativeSendUncertain`. After handoff, the result never matches `ErrNativeSendRefused`.

## Window after admission

The last authority decision is made when `Admit` calls `write`. After that, the frame waits for
the Noise write lock (not interruptible), then the Noise writer checks `sendCtx`. A revocation
that cancels `sendCtx` before this check refuses the write without consuming a nonce. After the
check, the frame is encrypted and written by a goroutine whose WebSocket write uses the
connection's lifetime context, not `sendCtx`. Cancelling `sendCtx` then makes the send return an
error while the frame can still go out, so that outcome is unknown. The writer-lock wait is the
same wait every ordinary send has today.

## Library-added send metadata

With `SendReportingTokens` enabled, HyperMeow adds a message secret and a reporting-token node,
and it may add a privacy token (`tctoken` or `cstoken`). These come from the library, not from
the guarded content. Integrators that pin "no send-time additions" must accept these explicitly
or keep reporting tokens off for guarded sends.

## Retries

Guarded sends do not populate the retry message cache and do not use the disconnect
`retryFrame` resend. A disconnect response returns `ErrNativeSendUncertain`. Integrators must
keep their `GetMessageForRetry` fallback from returning guarded content and must not wrap this
API in an automatic retry. Durable retry, receipt and outbox policy belongs to the integrating
application.
