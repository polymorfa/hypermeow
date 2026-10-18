# Optional native send admission (dark integration seam)

`SendRequestExtra.NativeSend` is process-local and nil by default. The existing
send path is unchanged when nil. Initial support is one ordinary text DM with
an explicitly preallocated message ID; group, peer, bot, media and extra-node
paths are refused before preparation. This is not a renderer or a grant.

`Admit(ctx, id, write)` runs after native preparation and after the Noise socket
write lock. It must synchronously invoke `write` at most once inside the final
local/authority fence. Returning without invocation refuses the send. A retained
callback cannot invoke after Admit returns. A refusal consumes no Noise nonce
or first-frame header. `Entered()` is a non-blocking notification immediately
before the actual WebSocket write call; it is evidence of entry, not provider
acceptance. It cannot prove whether bytes reached a provider.

The hook returns before the provider ACK wait. Callers must not hold Campaign
locks through that wait. Persist an invocation CAS marker and preallocated ID
before writing; an existing marker with no durable outcome is unknown and must
never be reinvoked. No goroutine-start or AMQP-publish callback counts as entry.
A frame-write failure or post-entry authority loss is not proof of non-delivery.

Guarded sends do not populate HyperMeow's retry content cache/store and do not
use the disconnect frame resend path. Integrators must also keep their
`GetMessageForRetry` fallback from returning guarded Campaign content and must
not implement an automatic retry around this API. Durable retry/receipt policy
and the regional invocation/result outbox belong to the owning application.
No Campaign consumer, positive authority adapter, database schema or runtime
mount is supplied here. This seam alone does not establish native acceptance.
