# HyperMeow

[![Go Reference](https://pkg.go.dev/badge/github.com/polymorfa/hypermeow.svg)](https://pkg.go.dev/github.com/polymorfa/hypermeow)
[![Go](https://github.com/polymorfa/hypermeow/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/polymorfa/hypermeow/actions/workflows/go.yml)

HyperMeow is Polymorfa's production-focused fork of [tulir/whatsmeow](https://github.com/tulir/whatsmeow). It keeps the upstream Go package names and protocol foundation while adding the business-app surface, LID-first identity model, bounded state, PostgreSQL efficiency, and reliability controls needed for large multi-session deployments.

The upstream project remains the smaller choice for applications that only need its core WhatsApp messaging scope. HyperMeow deliberately accepts a broader API and maintenance surface in exchange for the capabilities below.

## Why HyperMeow instead of upstream WhatsMeow?

This comparison was last verified against upstream `main` at [`a23afe3`](https://github.com/tulir/whatsmeow/commit/a23afe3171803f34d6761979988b9d2275e961c7) on 2026-08-11. Each upstream sync must update this section when the difference changes.

| Area | HyperMeow advantage over upstream `main` |
| --- | --- |
| Business app | Adds linked-account and eligibility reads, business-profile and cover-photo mutation, product and collection mutation, catalog creation, cart and visibility controls, appeal and merchant-compliance operations, and validated business message builders. |
| Native Flows | Adds typed address, list, order, and Flow message builders with payload limits, exact JSON-number preservation, UTF-8 validation, and response metadata handling. |
| Identity | Treats LIDs as the stable Signal identity, persists PN and username aliases, resolves aliases in batches, exposes phone-number consent messages, and generates LID identity verification codes. |
| App state | Adds atomic label replacement, label and quick-reply events, full-sync event controls, and independent history-sync receipt, storage, and media-deletion policies. |
| Channels | Adds newsletter deletion through generated MEX bindings. |
| Reliability | Hardens malformed binary-node handling, serializes device save/delete operations, surfaces participant-hash mismatches, bounds attacker-influenced caches, and redacts sensitive business payloads from node logs. |
| PostgreSQL | Batches Signal and metadata work, avoids empty PN-to-LID transactions, adds indexed alias lookups, and keeps PostgreSQL authoritative after bounded cache eviction. |
| Validation | Ships a reproducible Barback/PostgreSQL harness covering DM, group, history, media, mixed-message, security-code, phone-consent, resource, and saturation workloads. |

### Measured system advantages

The repository retains the raw reports and methodology behind these numbers in [`benchmark/barback/testdata/results`](benchmark/barback/testdata/results). They compare frozen revisions under the same local Barback, PostgreSQL, TLS, Noise, CPU, and memory constraints. They are engineering comparisons, not WhatsApp WAN or account-rate-limit claims.

| Measurement | Upstream WhatsMeow | HyperMeow | Result |
| --- | ---: | ---: | ---: |
| Disconnected constructor heap, 2,000 clients | 78,929 B/client | 4,240 B/client | 94.6% less Go heap |
| Group-128 send p95 | 33.998 ms | 7.024 ms | 79.3% lower |
| Group-128 client CPU | 5.502 s | 2.301 s | 58.2% lower |
| Group-128 SQL calls | 165,053 | 3,456 | 97.9% fewer |
| Highest healthy encrypted ping-pong rate | 900 pairs/s | 1,700 pairs/s | 1.89x the rate |

The complete three-repeat system matrix documents allocation, RSS, CPU, latency, PostgreSQL, network, and I/O results across five workloads. It also records the trade-offs: history-heavy peak RSS was slightly higher in one comparison, and active-session capacity depends on contacts, groups, media, handlers, and application queues rather than constructor memory alone.

- [System comparison](benchmark/barback/testdata/results/system-comparison.md)
- [RAM hardening](benchmark/barback/testdata/results/ram-hardening.md)
- [Ping-pong saturation](benchmark/barback/testdata/results/maxrate-ping-pong.md)
- [Benchmark instructions](benchmark/barback/README.md)

## Install

Install the newest reviewed commit from the authoritative `main` branch:

```sh
go get github.com/polymorfa/hypermeow@main
```

The `main` query resolves to a commit pseudo-version. Pin the resulting
pseudo-version in `go.mod` for reproducible builds. Every published semantic
version is retracted: do not install HyperMeow with `@latest`, a version tag, or
any feature branch.

To experiment with changes that have not reached `main`, use the integration
branch explicitly:

```sh
go get github.com/polymorfa/hypermeow@dev
```

`dev` is unstable and may be rebased. Do not use it in production or publish a
library that depends on it.

The root package remains named `whatsmeow`, so use an explicit import alias:

```go
import whatsmeow "github.com/polymorfa/hypermeow"
```

## Migrating from WhatsMeow

Replace `go.mau.fi/whatsmeow` imports with `github.com/polymorfa/hypermeow` and remove any old module replacement:

```sh
go mod edit -dropreplace=go.mau.fi/whatsmeow
go get github.com/polymorfa/hypermeow@main
go mod tidy
```

## Core compatibility

HyperMeow retains upstream's core support for:

- private, group, media, and status messages;
- group management and invite links;
- typing, delivery, and read receipts;
- app-state synchronization;
- retry receipts and message decryption recovery.

New exported functionality is additive unless called out in the [changelog](CHANGELOG.md).

### Remote Signal stores

`NewRemoteSignalClient` uses a customer-held Signal store through `RemoteSignal`.
The store supplies public prekeys and builds encrypted message stanzas; private
keys and message plaintext stay outside HyperMeow. Applications implementing the
interface must implement both durable upload operations:

- `PreparePreKeyUpload(ctx, count)` returns a `RemotePreKeyUploadBatch` with an
  opaque 32-character lowercase hexadecimal `BatchID` and 1–812 public `PreKeys`.
  `count` selects a fresh batch size. An outstanding batch returns all surviving
  keys in its original order, even when its size differs from `count`. Persist
  that batch across service restarts; do not truncate it or generate a top-up.
- `AcknowledgePreKeyUpload(ctx, batchID, ids)` commits the complete upload after
  the WhatsApp upload IQ succeeds. IDs can cross the 24-bit wrap boundary. The
  acknowledgement covers every surviving key; consumed original members can
  remain in the list and must not be recreated. Replaying the last exact ACK
  after a lost response succeeds without changing a newer batch.

The adapter captures independent public-key and ID snapshots. It checks the
literal last uploaded ID and acknowledges the entire captured list. A failed IQ
does not acknowledge the batch. After an attempted ACK fails, the adapter retains
it and resolves that exact ACK before preparing another batch. Unsupported remote
operations fail; there is no fresh-generation fallback. Upload serialization and
the adapter mutex keep upload state separate from retry-receipt generation.

`GeneratePreKeys` and `MarkPreKeysUploaded` remain separate operations for fresh
retry-receipt keys. `GenOnePreKey` generates one fresh key and marks it published;
it never borrows from a prepared upload batch. Remote implementations must reject
legacy marking of prepared-batch members. Returned one-time prekeys must have
unique nonzero 24-bit IDs, a nonzero 32-byte public key, and no private key or
signed-prekey signature.

This interface change requires matching remote-store implementations. It does
not establish that a service is deployed or a feature is available to customers.
Production consumers continue to pin reviewed `main` pseudo-versions.

## Discord

Join the [WhatsApp Web ecosystem Discord](https://whiskey.so/discord) and visit
`#hypermeow` for project discussion.

## Get support

If you'd like business to enterprise-level support from Rajeh, you can book a video
chat. Book a 1 hour time slot by contacting him on Discord or pre-ordering
[here](https://purpshell.dev/book). The earlier you pre-order the better, as his
time slots usually fill up very quickly.

If you are a business, we encourage you to contribute back to the development costs
of the project. You can do so by booking meetings or sponsoring below. All support
is welcome from businesses of all sizes.

## Sponsor

If you'd like to financially support this project, you can do so
[here](https://purpshell.dev/sponsor).

## Disclaimer

> [!CAUTION]
> This project is not affiliated, associated, authorized, endorsed by, or in any way
> officially connected with WhatsApp or any of its subsidiaries or its affiliates.
> The official WhatsApp website can be found at whatsapp.com. "WhatsApp" as well as
> related names, marks, emblems and images are registered trademarks of their
> respective owners.
>
> The maintainers do not condone using this project in practices that violate the
> Terms of Service of WhatsApp, and call upon users to use it fairly.

## License

Upstream-derived files retain their original Tulir Asokan and contributor
copyright notices and remain licensed under [MPL-2.0](LICENSE).

Polymorfa-added files: Copyright (c) 2026 Rajeh Taher, licensed under the
[MIT License](LICENSE-MIT).

See [LICENSING.md](LICENSING.md) for the file-level boundary.

### Connection lifecycle observers

`AddConnectionEventHandler(func(context.Context, any))` observes `Connected`,
`Disconnected`, `QR`, `LoggedOut` and `StreamReplaced`. The context is the original
connection's `ConnectContext` argument, captured before asynchronous delivery.
A delayed event can arrive after that context is cancelled. Compare it with the
connection authority your application owns before updating state; do not label an
old event using the newest connection. `RemoveConnectionEventHandler(id)` removes
a registration; an already captured callback can still arrive. Callbacks run
without the observer registry lock and can remove themselves.

Generic `AddEventHandler` consumers still receive these events. Other event types
retain their existing API. Connection observers do not authorize a send, undo
bytes already sent or prove that a cancelled socket has finished closing.
Disconnect and wait for owned work to settle before accepting replacement
connection authority. `FrameSocket.SetDisconnectHandler` synchronizes callback
ownership with socket shutdown; assign the legacy field only before connecting.
These additions are unreleased until their reviewed main commit is adopted.
