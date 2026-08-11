<!--
Copyright (c) 2026 Rajeh Taher
Licensed under the MIT License. See LICENSE-MIT for details.
-->

# Direct-message benchmark matrix

On 2026-08-06, the baseline library at `61888a0` and the refined HyperMeow working tree were run against Barback `9e7d0dc` with the same PostgreSQL image, verified TLS and Noise certificates, and 2-vCPU/3.5-GB Compose limits. Every scenario started with a new database, device, and certificate set.

| Scenario | Senders | Offered rate | Messages | History received |
| --- | ---: | ---: | ---: | ---: |
| Steady peer | 1 | 60/s | 600 | 0 |
| Parallel peers | 8 | 100/s | 800 | 0 |
| Parallel peers | 32 | 120/s | 960 | 0 |
| Parallel peers | 64 | 160/s | 1,280 | 0 |
| Burst | 16 | 400/s | 1,600 | 0 |
| History plus DM | 8 | 80/s | 400 | 8,000 |

Both revisions sent and received all 5,640 direct messages with no send failures or queue overflows. Completed throughput differed by less than 0.5% in every pair because Barback controlled the offered rate.

| Scenario | p95, baseline → HyperMeow | SQL calls | PostgreSQL time | Client CPU | Go allocation | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Steady peer | 3.33 → 3.38 ms | -49.3% | +5.1% | +3.5% | +1.0% | +1.1% |
| 8 peers | 3.27 → 3.29 ms | -49.0% | -1.2% | -4.1% | +0.8% | +0.8% |
| 32 peers | 3.26 → 2.16 ms | -47.8% | -27.6% | -22.1% | +0.7% | 0.0% |
| 64 peers | 2.67 → 1.88 ms | -47.0% | -25.9% | -16.1% | +0.9% | +5.3% |
| 16-peer burst | 1.91 → 1.37 ms | -49.2% | -26.1% | -19.0% | +0.9% | +0.1% |
| History plus DM | 4.16 → 3.28 ms | -74.3% | -60.0% | -16.0% | +6.7% | +3.5% |

The steady and eight-peer differences are within single-run host noise. The 32-peer, 64-peer, burst, and history results agree on lower database work, CPU cost, and tail latency. Database snapshots differed by less than 0.4% in every DM pair, so the group workload's PostgreSQL relation-growth outlier did not reproduce here.

## Memory profile

Full-rate burst profiles show about 224 MB of cumulative allocation and 3 MB of live heap for the refined build. There is no retained-heap growth tied to message count. Removing transaction wrappers around single-statement batches saved about 4.7 MB versus the first HyperMeow candidate. The final profile allocates about 3.6 MB more than baseline, under 1% of the combined profile volume.

The remaining delta is concentrated in pgx's byte-array encoding and Bind-frame construction for a two-row Signal-session upsert. Splitting that upsert removes the allocation but restores a second database round trip for every reply. The batched form was retained because the DM matrix shows lower database time and CPU at load with essentially flat peak RSS.

Common allocations still include Signal session JSON serialization, pgx result copies, SHA/HMAC state construction, and libsignal debug-message formatting. These need dependency or serialization changes and were not altered without separate compatibility evidence.

## Refinement

The post-profile refinement:

- lets cached identity checks use concurrent read locking while retaining exclusive miss and mutation paths;
- caps each device's identity cache at 8,192 entries, with PostgreSQL remaining authoritative after eviction;
- avoids opening a transaction for session or message-secret batches that fit in one atomic SQL statement;
- keeps transactions for multi-statement chunks;
- records the workload in each result and stops throughput timing at the final completed reply instead of after the two-second metrics-settling delay.

The complete Go suite, store race tests, benchmark race tests, and all refined DM scenarios passed.
