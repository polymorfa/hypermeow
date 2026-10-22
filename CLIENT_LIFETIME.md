# Final client close

`(*Client).FinalClose(ctx)` permanently seals one exact Client instance. Pass a
context with a deadline. `Disconnect` keeps its reusable behavior; it does not
seal the instance. Neither operation logs out or deletes the device store.

This is local lifecycle source, not a published contract or accepted deployment
producer. **FinalClose never returns nil.** Once the tracked subset joins it
returns `ErrClientShutdownIncomplete`. Context cancellation/deadline errors mean
that this waiter did not observe even that subset's completion. A cancelled
invocation before sealing makes no lifecycle change. A later invocation joins
the same teardown, rather than retrying it. No stop receipt may be derived from
any of these errors, a socket disconnect, or a cancelled context.

Concurrent callers share one teardown of the same pointer. Replacement Client
instances have separate lifetimes. Do not copy Client values. No client or socket
state lock is held across a completion wait. A blocked external transport or
callback can keep the owned teardown alive after a caller's deadline. Calling
FinalClose from a callback cannot finish joining that callback until it returns;
the deadline ends its waiter and does not manufacture completion.

## Tracked native subset

- ConnectContext and internal reconnect attempts, including admitted dials and
  handshakes. Final close interrupts their contexts and refuses future attempts.
- Keepalive and handler-queue loops, connection-triggered disconnected/reconnect
  tasks, handler-queue reset tasks, and each scheduled node handler.
- Synchronous receive processing, RawNodeHandler, dispatchEvent and each admitted
  event handler, plus AutoReconnectHook invoked by tracked reconnect work.
- FrameSocket read pump, NoiseSocket consume loop, socket disconnect callbacks,
  asynchronous socket close, and asynchronous writes. Client installs the socket
  launcher before dialing. The launcher records owned completion before scheduling.
  A write remains tracked after SendFrame returns on context cancellation.
- Connection-event async dispatch wrappers, the 515 disconnect/reconnect task,
  and the connect-success task. Context-aware tasks acquire an owned forwarding
  context before scheduling; their existing store/prekey/IQ bodies are unchanged.
- GetQRChannel registration and its one cleanup worker, each admitted QR emitter,
  output sends, terminal closure and exact handler removal. The channel lifetime
  starts before Connect, including when no QR event ever arrives. Caller/final
  cancellation ends that lifetime. Ordinary Disconnect stops an emitter as before
  and leaves the channel available for subsequent events.
- Async notification dispatches, server AB-props tasks and their ACK callback,
  privacy-token issuance/persistence, unified-session children and expired-token
  pruning, including the connect-success prune. See the exact boundaries below.
- One teardown task, whose completion is recorded before launch. Context forwarding
  uses context.AfterFunc only to cancel contexts, with no user/protocol callbacks;
  those small cancellation callbacks are not a joined product-worker claim.

A permanent seal prevents new admissions to this subset. Work admitted before
sealing may execute after sealing and remains counted until its function returns.
Frame delivery can exit when the connection context is cancelled, rather than
remaining stuck sending to an abandoned consumer. Socket ciphertext, counters,
keys, handshake and wire formats are unchanged. Standalone FrameSocket users keep
the default launcher unless they explicitly install their own before Connect.

## Forwarding context ownership

Each connect attempt owns a forwarding context and releases that ownership on
return, including failed, deleted-store and already-connected paths. If initial
background reconnect is requested, ConnectContext returns nil only when that
reconnect task was actually admitted. Refused admission returns ErrClientClosed.
The admitted task retains the context before scheduling and releases it on exit.
Its internal attempts reuse the same forwarding scope instead of adding one
watcher per retry. Once no owner remains, cleanup unregisters the lifetime
AfterFunc and cancels the derived context.

A successful handshake transfers a context reference to the client lifetime
before ConnectContext can return. These established contexts remain owned until
final close, including across ordinary Disconnect. This is conservative: the
unbound producers below may still inherit an established event context after its
socket workers exit. Their exact completion is not known, so early cancellation
would change ordinary behavior. Reconnect attempts with that inherited context
reuse the same scope. Explicit new successful connections with different caller
contexts can retain separate scopes until final close. This is not a claim of
complete context/producer joins or bounded successful-connection inventory.

Final close cancels the lifetime outside state locks, waits for the tracked
subset, and releases the established-context references. No successful return or
stop receipt follows from that cleanup. Tests of the context-transfer helper do
not establish a live handshake/socket acceptance result.

## QR channel closure and boundaries

One reserved cleanup worker owns each output close and handler removal. Sends
register under the QR-local mutex, then wait without it. Closure seals new sends
and waits for existing sends outside locks before closing output. Terminal event
callbacks are released before cleanup acquires the handler-list write lock, so
removal cannot deadlock on the callback's own read lock. FinalClose joins the
cleanup and emitter tasks even if removal is blocked by another callback.
QR-local closure and emitter admission also share one lock. Timers retain the
existing 20-second/60-second values and are stopped on every interrupted wait.

Caller cancellation or final close can close output without a terminal pairing
status. Output backpressure keeps its prior nonblocking code-emission shutdown
behavior. A caller must not treat output close as a successful pairing, a joined
whole-client lifetime or a stop receipt. The cleanup worker can still be removing
its handler after output closes; FinalClose tracks that completion separately.

Expiry/caller cancellation may disconnect only the exact socket pointer observed
by the QR emitter if it is still current. No observed socket means no disconnect;
a retired pointer cannot stop a replacement socket on the same Client. This is
local lifetime protection, not authenticated event-origin or placement custody.
Synthetic injected QR events and externally started operations remain the caller's
responsibility. No pairing/protocol/Signal/key/store body or payload changed.
The existing automatic passkey-confirmation method is invoked synchronously by
its admitted QR callback, not reclassified as an accepted Signal producer.

## Notification and privacy task ownership

The five async notification dispatches (encrypt, server_sync, account_sync,
link_code_companion_reg and crsc_continuation) register before scheduling and
own their forwarding contexts until the called handler returns. Their existing
handler bodies remain unchanged, apart from the encrypt handler's privacy child
launch. Child producers inside those handlers still require the boundaries below.

Server AB-props tasks retain the original two-minute bound and pending-ID
coalescing. Refused admission removes the reserved pending entry. Task exit also
removes it on success, failure or cancellation. ACK admission checks the final
seal independently of asynchronous context forwarding, and the task counts the
actual ACK callback until return. Refusal withholds ACK; it never reports sync
success. No pending-map lock is held during the handler or completion wait.

Privacy issuance/persistence and unified-session children keep their original
BackgroundEventCtx ancestry and values. They now derive a forwarding context for
final cancellation and stay counted through the actual IQ/store/send return,
including a store or transport that ignores cancellation. The synchronous
DangerousInternals entry points keep their original signatures and Background
semantics; they remain caller-owned. Both presence and pairing's unified child
launches are gated, but the outer pairing producer and PrePairCallback remain
unbound. No pairing body or protocol payload changed.

Expired-token pruning, including the connect-success child, registers before
launch. Its existing TryLock guard coalesces work until the store actually
returns. Refused admission releases that guard without consuming the prune
timestamp. An admitted task records the original captured timestamp under the
guard. The 24-hour interval, cutoff algorithm and store body are unchanged.
Ordinary Disconnect does not seal or cancel these background tasks.

External RefreshCAT implementations, store/transport internals and user-spawned
callbacks do not gain whole-client completion proof from these wrappers. The
established-context retention described above remains conservative and unchanged.

## Still unbound

The following existing client-owned producers retain their prior behavior and
are NOT included in the completion claim. Some use BackgroundEventCtx or
WithoutCancel and can continue launching child work after the core seal. This
is why the implementation cannot confirm full termination even when its tracked
count reaches zero. Their admission, cancellation and actual completion need
owning-library work before any successful stop result is possible.

| Source at base 29a42459 | Unbound lifetime |
| --- | --- |
| message.go:82-89,373-374,469-470,520,549,770,887,1005-1037 | Name updates, retry/ack work, event-buffer cleanup, event-dispatch wrapper, history download/storage/notification loops, key-share and peer-data work. |
| appstate.go:162,645,659 | Missing-key request and async event batches. |
| pair.go:138-147 | Outer pair completion and PrePairCallback. Its unified-session child is now tracked; the parent and its other boundaries are not. |
| receipt.go:27,36,109,128; retry.go:469,541 | Async ack/retry handlers and delayed phone rerequests. Cancellation of delayed requests is not their join. |
| handshake.go:98-103 | GetClientPayload callback admission is not separately sealed; an admitted connection can reach it. The enclosing connect is counted, which is not full callback-admission coverage. |

Caller-invoked public API methods, DangerousInternals, external HTTP transports,
user store implementations and goroutines launched by user callbacks remain
separate caller-owned work. Their owner must stop and join them separately.
The socket wrapper tracks HyperMeow's explicit goroutines; it does not assert
joined internals for the third-party websocket/HTTP transport. No Signal
protocol, identity, state, crypto, custody, funding or placement authority is
added by this lifecycle source.

API integration, exact client-incarnation custody, runner producers, WAM,
BanSafe, Calls, Region and full deployment composition remain separate gates.
