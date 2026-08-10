# Changelog

All notable HyperMeow changes are documented here. HyperMeow follows semantic versioning for its own module beginning with `v0.1.0`.

## [Unreleased]

### Documentation

- Added an evidence-backed comparison with upstream WhatsMeow.
- Consolidated the root white-box test suite into themed files without removing tests or changing coverage.
- Moved committed benchmark reports and heap profiles into `benchmark/barback/testdata/results`; fresh run output remains under the ignored `benchmark/barback/results` workspace.

## [0.1.0] - 2026-08-10

The first tagged HyperMeow release combines the reviewed `dev` train with upstream WhatsMeow protocol updates through protobuf revision `v1044834443`.

### Added

- Published the standalone `github.com/polymorfa/hypermeow` module while retaining the upstream `whatsmeow` package names.
- Added raw-node compatibility hooks for integrations that need protocol-level observation.
- Added business linked-account and feature-eligibility reads.
- Added business profile, cover photo, product, collection, catalog, cart, visibility, appeal, and merchant-compliance mutations.
- Added validated business message builders for product lists, orders, addresses, lists, and native Flows.
- Added native Flow response metadata handling and exact preservation of JSON number lexemes.
- Added newsletter deletion through generated MEX bindings.
- Added quick-reply app-state actions and events.
- Added atomic label replacement and configurable label/full-sync event emission.
- Added independent history-sync receipt, persistence, and media-deletion controls.
- Added phone-number consent request/share message builders.
- Added LID identity verification-code generation.
- Added username persistence and PN/username-to-LID alias resolution across contacts, groups, notifications, and group membership changes.
- Added optional batched reverse-LID lookup support with bounded query chunks and negative caching.
- Added a reproducible Barback/PostgreSQL benchmark suite for DM, group, history, media, mixed messages, client memory, saturation, phone consent, business operations, and security codes.

### Changed

- Made LIDs the primary Signal identity while retaining phone numbers and usernames as aliases when available.
- Reworked retry-message storage to allocate lazily and retain encoded payloads instead of full protobuf object graphs.
- Added bounded group, device, contact, identity, migration, retry, app-state-key, and PN-to-LID caches with durable stores remaining authoritative.
- Batched PostgreSQL session, identity, message-secret, and alias operations while retaining atomic fallbacks for generic SQL drivers.
- Avoided empty PN-to-LID database transactions and added PostgreSQL pattern indexes for migration-prefix lookups.
- Shared the default HTTP transport across clients while preserving isolation for custom proxy and HTTP-client configuration.
- Preserved the newest history-sync nonce through asynchronous persistence without blocking event delivery.

### Reliability

- Hardened malformed and odd-length binary node decoding.
- Serialized device save/delete operations and made overlapping history-sync nonce writes monotonic.
- Exposed participant-hash mismatches instead of silently accepting inconsistent group state.
- Added bounded handler queues and guarded reconnect behavior at overflow.
- Replaced unsupported Signal-store panics with returned errors.
- Added explicit error handling for receipt misuse, CBC invariant failures, missing signed prekeys, upload read failures, partial writes, and temporary-file cleanup.
- Added compatibility checks and Docker build support for archived WhatsMeow and pre-optimization Barback baselines after the module-path migration.

### Security and privacy

- Redacted business access tokens, cookies, nonces, profile fields, and linked-account payloads from binary-node logs.
- Added strict validation for business payload lengths, prices, product/section counts, mixed native-flow metadata, UTF-8, and trailing JSON.
- Added privacy-cache completeness and bounded failure behavior.
- Added atomic Signal identity insertion and deletion-generation fencing to prevent stale prekey fetches from restoring deleted identities.

### Performance evidence

- The frozen three-repeat system matrix completed 45 bounded workloads with no send failures, queue overflows, or temporary files left behind.
- Against the recorded upstream revision, HyperMeow reduced allocation in all five system scenarios, reduced group-128 send p95 by 79.3%, reduced group-128 client CPU by 58.2%, and reduced group-128 SQL calls by 97.9%.
- The isolated 2,000-client constructor benchmark measured 4,240 bytes of Go heap per HyperMeow client versus 78,929 bytes upstream, a 94.6% reduction in disconnected fixed state.
- The encrypted ping-pong test sustained 1,700 healthy pairs per second versus 900 upstream under the same bounded local environment.

See the [system comparison](benchmark/barback/testdata/results/system-comparison.md), [RAM hardening report](benchmark/barback/testdata/results/ram-hardening.md), and [saturation report](benchmark/barback/testdata/results/maxrate-ping-pong.md) for revisions, limits, caveats, and raw evidence.

### Compatibility notes

- Applications must migrate imports from `go.mau.fi/whatsmeow` to `github.com/polymorfa/hypermeow`.
- A single binary must not link both modules because they register identical generated protobuf descriptors.
- The Polymorfa Signal dependency changes exported Signal parameter types relative to upstream; other exported differences in this release are additive.

[Unreleased]: https://github.com/polymorfa/hypermeow/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/polymorfa/hypermeow/releases/tag/v0.1.0
