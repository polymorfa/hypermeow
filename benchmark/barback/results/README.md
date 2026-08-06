# Group-128 secure benchmark

Two clean runs per revision used the same Barback revision, PostgreSQL image, TLS and Noise verification, 2-vCPU/3.5-GB Compose limits, 128 additional group members, 200 messages at 50 messages per second, and four history-sync payloads containing 8,000 messages total. Every run sent and received all 200 messages with no send failures or queue overflows.

| Metric, mean of two runs | Baseline | HyperMeow | Change |
| --- | ---: | ---: | ---: |
| Recorded throughput | 18.72 msg/s | 22.39 msg/s | +19.6% |
| Send latency p95 | 33.68 ms | 8.63 ms | -74.4% |
| WhatsMeow SQL calls | 85,853 | 2,056 | -97.6% |
| PostgreSQL execution time | 630.35 ms | 243.37 ms | -61.4% |
| Client CPU time | 2.76 s | 1.38 s | -49.8% |
| Cumulative Go allocation | 832.39 MB | 939.18 MB | +12.8% |
| Peak client RSS | 60.35 MB | 62.11 MB | +2.9% |
| Database size at snapshot | 15.34 MB | 20.63 MB | +34.5% |

The database-size increase is PostgreSQL heap bloat from repeatedly updating the same 130 live Signal-session rows in large statements. Autovacuum removes dead tuples but does not shrink the relation file. A tested 32-row session batch reduced allocation but produced more bloat and SQL calls without improving latency, so it was rejected.

These archived group results predate the benchmark's corrected completion boundary. Their recorded throughput includes a two-second metrics-settling delay and must not be used for capacity estimates. The latency, SQL, CPU, allocation, RSS, and database-size measurements are unaffected.

The accepted change is a database-load and latency improvement, not a memory or storage-footprint improvement. Capacity decisions need longer soak tests on the target VPS, including relation growth, autovacuum behavior, reconnect churn, and multiple simultaneous clients.

Raw reports:

- `baseline-secure-1-g128.json`
- `baseline-secure-2-g128.json`
- `candidate-secure-1-g128.json`
- `candidate-secure-2-g128.json`

The full-rate allocation comparison and portable pprof captures are documented in `memory-g128.md`.

The direct-message matrix, refinement, and burst memory profile are documented in `dm-matrix.md`.
