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

HyperMeow uses commit pseudo-versions from its reviewed `main` branch:

```sh
go get github.com/polymorfa/hypermeow@latest
```

Only `main` is authoritative for package documentation and pseudo-version resolution. `dev` is an integration branch.

The root package remains named `whatsmeow`, so use an explicit import alias:

```go
import whatsmeow "github.com/polymorfa/hypermeow"
```

## Migrating from WhatsMeow

Replace `go.mau.fi/whatsmeow` imports with `github.com/polymorfa/hypermeow` and remove any old module replacement:

```sh
go mod edit -dropreplace=go.mau.fi/whatsmeow
go get github.com/polymorfa/hypermeow@latest
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

## Tests and repository layout

Go `_test.go` files stay beside the package they test. Most HyperMeow tests intentionally exercise unexported concurrency, cache, parser, and persistence behavior; moving them into a separate directory would stop Go from treating them as the same package.

To keep the repository root navigable, the white-box suite is consolidated into five themed files—business, identity, app state/history, messaging, and client runtime—plus the external client contract suite. Consolidation changes file layout only; the complete test inventory and coverage remain intact.

The handwritten business-app API is consolidated in `business.go`; generated protocol bindings remain separate.

Versioned benchmark reports and heap profiles live under `benchmark/barback/testdata/results/`. Fresh local runs write to the ignored `benchmark/barback/results/` workspace.

## Documentation and discussion

- [Go API reference](https://pkg.go.dev/github.com/polymorfa/hypermeow)
- [Changelog](CHANGELOG.md)
- Discord: [whiskey.so/discord](https://whiskey.so/discord), `#hypermeow`
