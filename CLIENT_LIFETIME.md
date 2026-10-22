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
- Client-owned message name/event/history/storage/receipt/key-share launches,
  ACK/retry handlers and delayed phone rerequests. Their exact contexts and loop
  ownership are described below. The appstate children have their own boundary.
- Appstate missing-key request and the two contextless send/resync event batches,
  including their actual synchronous callbacks and request/store return.
- Outer pairing task through handlePair and its existing error/success
  continuations; PrePair decision/callback and handshake payload selection/custom
  callback through actual synchronous return. See the continuation boundary below.
- One teardown task, whose completion is recorded before launch. Context forwarding
  uses context.AfterFunc to cancel derived contexts. Exact completion entries
  now join those existing forwarder invocations through actual cancel return;
  stopping their registration alone is not completion.

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
watcher per retry. Once no owner remains, cleanup keeps its original lifetime
AfterFunc stop followed by derived cancel. Stopped registrations retire at once;
an already-started forwarder retains a separate completion entry until its actual
cancel invocation returns.

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

## Forwarder actual-return ownership

A nonzero-size entry on the exact client lifetime is registered before AfterFunc
can schedule its callback, including when an already admitted constructor reaches
a canceled lifetime. The callback keeps asynchronous AfterFunc behavior and calls
only the original derived cancel, with deferred entry completion. Its completion
does not read or depend on publication of the stop function. Stop-before-start
resolves that exact entry once; an already-running or completed callback makes
stop return false without pretending it joined. Entries retire on stopped-before-
start or actual callback completion, rather than retaining completed registrations.

Entries are separate from active reservations and successful context pins. A
persistent registration counted as active would cycle with pins released only
after the active join. Final teardown retains its existing cancellation,
Disconnect and exact socket-close order, joins active work, detaches/releases
pins, then snapshots and drains remaining entry completions outside lifetime,
context, socket, store and global locks before closing stopped. Production
nativeContext constructors in ConnectContext, connect, startConnectionTask,
GetQRChannel and the synchronous history-loop adapter all have an active owner
before construction and release their context before completing that owner.
Therefore no production constructor can add a registration after that drain
frontier. Private helpers are not a new public admission contract.

Ordinary last-reference cleanup does not acquire a new completion wait: it still
calls stop then cancel. Either call can have its existing blocking behavior,
including custom Context cleanup reached by cancel. Final teardown records an
already-started forwarder's actual return separately. A callback can finish before
its stop function is published, and bounded or canceled FinalClose waiters still
coalesce the same teardown. Reentry from a forwarder cannot join itself before
return and must end its bounded waiter without reporting completion.

SOURCE-1 is locally implemented only, pending new independent review. It does
not bound successful-context retention, cancel established scopes earlier, join
user/custom/transport/Signal descendants or establish native/custody acceptance.
Callback or custom Context return does not prove its spawned descendants ended.
FinalClose remains always incomplete; no successful stop receipt follows.

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
launches are gated. Outer pairing and PrePairCallback now have the separate
boundary below. No pairing algorithm or protocol payload changed.

Expired-token pruning, including the connect-success child, registers before
launch. Its existing TryLock guard coalesces work until the store actually
returns. Refused admission releases that guard without consuming the prune
timestamp. An admitted task records the original captured timestamp under the
guard. The 24-hour interval, cutoff algorithm and store body are unchanged.
Ordinary Disconnect does not seal or cancel these background tasks.

External RefreshCAT implementations, store/transport internals and user-spawned
callbacks do not gain whole-client completion proof from these wrappers. The
established-context retention described above remains conservative and unchanged.

## Message, history, ACK and retry task ownership

All explicit async launches in message.go, receipt.go and retry.go register
before scheduling. Typed helpers capture the original direct go-call arguments
before launch, including contact-name strings/JIDs, retry booleans, node/key-share
pointers and the identity event with its time.Now timestamp. The existing
anonymous history/peer receipt closures still read info.ID when executed.
Handler, crypto/key/store bodies and payloads are unchanged apart from the
inventoried scheduling/context/ownership controls.

Name tasks and ACK/retry handlers keep caller contexts and values. Retry sends,
async history storage and event-buffer cleanup keep WithoutCancel ancestry,
then add final cancellation independently. History loops and delayed phone work
keep BackgroundEventCtx ancestry. Every admitted task stays counted until its
actual store, callback, send or handler returns, even if it ignores cancellation.
Ordinary Disconnect does not permanently seal the reusable client or apply
final cancellation to all these scopes. It retains its existing cancellation of
pending delayed phone requests whose owners are registered in the cancel map.
That cancellation does not prove actual return or join, and does not mean every
scheduled or admitted task has stopped.

Synchronous ACK/name/storage branches retain their original inline execution
and contexts, including canceled flags, NACKs and error policy. They are part of
an already admitted enclosing operation or separate caller-owned work. The
contextless DangerousInternals async-function adapter joins the supplied function
but cannot cancel an unknown captured context or own user-spawned descendants.

History loop claims have exact nonzero-size owner tokens. Only that token can
release its claim or started indicator. Admission refusal releases its claim;
normal return/panic cleanup can restart queued work through a fresh admitted
owner. Restart logging runs inside the registered loop with cleanup installed.
The synchronous DangerousInternals loop adapter now takes the same exclusive
claim and joins its invocation; it cannot duplicate or clear another running
owner. This adapter keeps its signature and Background ancestry. Its other
caller-owned operations remain separate boundaries.

A blocked history enqueue keeps ordinary backpressure and ignores ordinary
caller cancellation as before. Permanent final close interrupts it without
closing the queue. The bounded queue can retain pending items on final close;
no new loop or receipt is admitted to process them. The download/event/media
branch and one-minute idle timeout are unchanged. A loop stops on final close
and never restarts after that seal, while its actual in-flight work remains joined.

Event-buffer cleanup retains the twelve-hour interval. A short mutex protects
its launch guard and timestamp, never a completion wait. Only admitted work
records the original captured time. Refusal releases the guard without advancing
that timestamp; actual store return releases it on every exit.

Delayed phone rerequests keep their five-second delay, feature checks, per-ID
coalescing, cancellation and retry policy. The cancel map stores exact owner
tokens. Cleanup removes only that owner even after cancellation/replacement;
canceling an entry is not its completion. Refused launch creates no entry. A
synchronous DangerousInternals delayed call retains its Background behavior and
is still caller-owned. No timers/retry counts/protocol algorithms changed.

## Appstate asynchronous children

The missing-key request and both send/resync event batches register before
scheduling. The request keeps the original pre-launch WithoutCancel context
and PatchList pointer as typed helper parameters. Final cancellation is added
through the existing owned forwarding context. Refused admission does not enter
GetMissingKeyIDs, initialize/update/prune the request cache, advance request
times, log a key request or send. Admitted work stays counted through the actual
lookup/request return, even when a store or send ignores cancellation. The
request body, lock, deduplication, 24-hour cooldown, cap and ordering are unchanged.

The two event-batch closures retain their original event-slice capture and range
order. A contextless batch owns every synchronous dispatch it makes until the
closure returns. Its private dispatcher keeps the existing handler-list locking,
callback status and panic recovery behavior, without reapplying the final seal
between already admitted members/handlers. This permits an admitted batch to
finish its original callbacks after sealing without dropping or reordering
members. New batches are refused after sealing. The general dispatchEvent
admission rules are unchanged; this private dispatcher must not escape to
independently spawned work. A user-spawned descendant is not joined by the batch.

Ordinary Disconnect does not permanently seal the client or cancel these new
appstate children; its registered delayed-phone cancellation exception above
still applies. Batch reentry into bounded FinalClose cannot join its own callback
until it returns, so a waiter error cannot acknowledge completion. No new state
lock is held while the lifetime engine waits. The existing handler-list read lock
around synchronous callbacks is retained, including its existing reentry limits.

Public FetchAppState/SendAppState and DangerousInternals synchronous operations
keep their signatures, errors and caller ownership. Only their named async
children gain this admission/join boundary. Appstate patch/decode/crypto/key/store
algorithms, retry recursion, protocol payloads and timers are unchanged. This is
not whole-client source closure, native acceptance, Signal ownership or a stop
receipt. Conservative established-context retention remains unchanged.

## Outer pairing and client callbacks

The outer handlePairSuccess closure registers with startConnectionTask before
scheduling. It keeps its original parsed local variables, props pointer and
caller-context ancestry/values, adding owned final cancellation. It remains
counted through handlePair and its original log/Disconnect/PairError or
unified-session/PairSuccess continuations. Refusal runs none of that async body.
The original synchronous parsing, time-offset update and possible props-parse
warning before launch are unchanged; this does not seal the entire caller API.

PrePair callback decisions and handshake payload selection take a synchronous
native reservation before invocation. Nil PrePair still allows normally, false
still reaches the unchanged local-rejection branch, and the custom payload or
built-in Store.GetClientPayload fallback keeps its original value/bytes. A normal
nil custom payload remains nil; it is not replaced with a default. Refusal is
ErrClientClosed, so a sealed payload request cannot marshal an invented payload.
Both helpers release their count on actual return or panic, without swallowing
or translating the original panic policy. There is no implicit cancellation
argument for either contextless public callback signature.

If final sealing is observed after the callback/selection returns, its result is
rejected with ErrClientClosed before the pairing persistence or handshake payload
marshalling continuation. A late true PrePair result is not accepted as a decision;
a late payload is not treated as success. The new closed branch is distinct from
the unchanged normal false rejection branch. A seal after that post-call check
can still race an already admitted continuation. The enclosing owned pair task
or connection remains counted until actual return; no rollback or arbitrary
revocation of already admitted store/protocol work is introduced.

ConnectContext's existing closed checks and successful-context pin guard remain
intact and are not bypassed by the new callback error path. Already admitted
handshake continuations remain subject to those existing guards; no stronger
atomic no-effect or revocation contract is introduced. Public/DangerousInternals parents
beyond their named children remain separately caller-owned. This does not add
whole-request admission or authenticated ingress/custody. Callbacks can spawn
independent work whose completion is not established by their own return.

No new state lock is held while waiting. Existing Connect holds its socket lock
while invoking the handshake callback, as before; FinalClose reentry from that
callback needs a bounded waiter and cannot join itself until it returns. The
waiter ending is never completion. Ordinary Disconnect remains reusable, with
its registered delayed-phone cancellation exception unchanged. Contextless
callbacks retain their original cancellation behavior and signatures. Client
callback fields gain no new concurrent-setter contract or copied/global owner.

## Still unbound

The bounded source groups described above do not establish an exhaustive
producer inventory or complete client termination. Other caller/custom/callback
boundaries and the complete bounded successful-context inventory remain open.
No additional producer group or independent native acceptance was established
by these wrappers. FinalClose remains unconditionally incomplete after its
tracked join; missing ownership cannot be inferred from a zero tracked count.

Caller-invoked public API methods, DangerousInternals, external HTTP transports,
user store implementations and goroutines launched by user callbacks remain
separate caller-owned work. Their owner must stop and join them separately.
The socket wrapper tracks HyperMeow's explicit goroutines; it does not assert
joined internals for the third-party websocket/HTTP transport. No Signal
protocol, identity, state, crypto, custody, funding or placement authority is
added by this lifecycle source.

API integration, exact client-incarnation custody, runner producers, WAM,
BanSafe, Calls, Region and full deployment composition remain separate gates.
