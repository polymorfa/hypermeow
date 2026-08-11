<!--
Copyright (c) 2026 Rajeh Taher
Licensed under the MIT License. See LICENSE-MIT for details.
-->

# RAM hardening results

On 2026-08-06, the PR #3 working tree was profiled again after the direct-message refinement. The comparison uses the same Barback revision, PostgreSQL image, verified TLS and Noise certificates, and 2-vCPU/3.5-GB Compose limits. Each run used a new PostgreSQL volume and paired device.

## Allocation profile

The full-rate DM burst sent and received 1,600 messages with no failures or queue overflows in both runs.

| Metric | Before RAM pass | After RAM pass | Change |
| --- | ---: | ---: | ---: |
| Cumulative Go allocation | 225.93 MB | 205.11 MB | -9.2% |
| Peak client RSS | 42.89 MB | 43.04 MB | +0.4% |
| Retained pprof heap | 2.93 MB | 2.83 MB | -3.4% |
| Garbage collections | 53 | 49 | -7.5% |
| Client CPU | 5.12 s | 5.19 s | +1.4% |
| Send latency p95 | 4.70 ms | 4.84 ms | +3.0% |
| PostgreSQL calls | 11,515 | 11,516 | +0.01% |
| PostgreSQL execution time | 105.49 ms | 112.18 ms | +6.3% |

The PostgreSQL session read path now deserializes from `sql.RawBytes` while the row buffer is valid. This removes the second `database/sql` copy without retaining driver-owned memory. The Signal dependency skips caller discovery and message formatting when its default debug logger is disabled. The final profile no longer attributes allocation to Signal debug logging or `bytes.Clone` in session reads. The CPU, latency, RSS, and PostgreSQL-time differences in this single pair are within host and PostgreSQL startup noise; the allocation reduction is visible in repeated refinement profiles and is the demonstrated improvement.

The 128-member group profile sent and received 200 messages and processed 8,000 history messages with no failures or queue overflows. Cumulative allocation fell from 895.92 MB to 796.77 MB (-11.1%), while PostgreSQL calls stayed fixed at 2,056. Retained pprof heap fell from 3.06 MB to 2.91 MB (-4.8%), and peak RSS fell from 66.48 MB to 57.29 MB (-13.8%).

## Retained state

The outgoing retry cache still covers 256 messages, but stores serialized protobuf bytes instead of retaining full 904-byte `waE2E.Message` objects and their pointer graphs. It is allocated on the first outgoing message instead of in `NewClient`. The exact isolated-container constructor benchmark measures 4,240 bytes per HyperMeow client, down from 78,929 bytes in upstream and pre-PR3. Across 2,000 clients, that saves 149,377,464 bytes of Go heap, or 142.46 MiB and 94.6%. A completely populated old retry ring retained at least 441 MB of outer protobuf structs across 2,000 clients before referenced payloads were counted; the compact cache replaces that cost with the encoded message size.

Attacker- or workload-influenced caches now have hard ceilings:

| Cache | Scope | Limit |
| --- | --- | ---: |
| Group metadata | client | 8 groups |
| User devices | client | 2,048 users |
| Contact records | SQL device store | 256 contacts |
| Signal identities | SQL device store | 2,048 addresses |
| PN session migrations | SQL device store | 1,024 users |
| Incoming and outgoing retry counters | client | 1,024 each per one-hour window |
| Session recreation history | client | 1,024 users, one-hour expiry |
| App-state key requests | client | 1,024 keys, 24-hour expiry |
| Shared PN↔LID mappings | PostgreSQL container | 65,536 pairs |

Eviction keeps PostgreSQL authoritative. Group and device cache misses may require a WhatsApp metadata query, while contact, identity, session, and PN↔LID misses use PostgreSQL. Retry counters and app-state key requests fail closed at capacity instead of evicting entries that enforce rate limits. The limits are high enough for 1,024-member group operations while preventing message IDs, app-state key IDs, contacts, and mappings from growing resident memory without bound.

## Regression matrix

The final build completed these clean runs:

| Scenario | Messages | History messages | Failures | Queue overflows | Peak RSS | Go allocation | SQL calls |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| DM burst, 16 peers | 1,600 | 0 | 0 | 0 | 43.04 MB | 205.11 MB | 11,516 |
| DM parallel, 64 peers | 1,280 | 0 | 0 | 0 | 33.53 MB | 166.47 MB | 9,844 |
| DM plus history, 8 peers | 400 | 8,000 | 0 | 0 | 62.22 MB | 98.74 MB | 3,027 |
| Group, 128 members | 200 | 8,000 | 0 | 0 | 57.29 MB | 796.77 MB | 2,056 |

The Go suite and focused store tests pass under the race detector. The Polymorfa Signal fork also passes its complete suite.

Raw final reports use the `ram-final-` prefix. The portable profiles are `ram-final-dm-burst-16.heap.pb.gz` and `ram-final-memory-g128.heap.pb.gz`.
