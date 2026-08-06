# HyperMeow Barback benchmark

This benchmark pairs a real WhatsMeow client with Barback, stores its device and Signal state in PostgreSQL, and drives group messages through the full Noise, Signal, sender-key, receipt, and history-sync paths.

The Compose profile caps the stack at 2 vCPU and 3.5 GB of service memory:

| Service | CPU | Memory |
| --- | ---: | ---: |
| PostgreSQL | 0.50 | 768 MB |
| Barback | 0.50 | 768 MB |
| HyperMeow client | 1.00 | 2 GB |

The remaining 512 MB represents operating-system and container-runtime headroom on a 4 GB VPS. CPU quotas make runs comparable on the same host; they do not make Apple Silicon identical to a specific cloud CPU. Use these runs for relative comparisons, then run the same Compose file on the target VPS before setting production capacity limits.

Run a clean benchmark:

```sh
docker compose down --volumes --remove-orphans
BARBACK_CONTEXT=/path/to/devcenter BUILD_REV="$(git rev-parse HEAD)" BENCH_VARIANT=baseline RESULT_FILE=baseline.json docker compose up --build --wait
docker compose wait client
docker compose down --volumes --remove-orphans
```

`BARBACK_CONTEXT` must point to a checkout of `titan-api/devcenter` at commit `9e7d0dc45faffc9fde6ab4f9fd405a3a61c0efe5`. The default resolves to the standard local Polymorfa checkout layout. Verify the commit before a comparison with `git -C "$BARBACK_CONTEXT" rev-parse HEAD`.

`LIBRARY_CONTEXT` can point to another HyperMeow worktree to compare two revisions without changing the benchmark code or branches. Barback generates a persisted TLS certificate for each clean stack. The client trusts that certificate and keeps both TLS and Noise certificate verification enabled.

Results are written to `results/`. PostgreSQL statement statistics are reset on the authenticated connection event, before Barback's three-second benchmark warmup. The report includes the top WhatsMeow queries, total statement calls and execution time, send latency percentiles, throughput, Go heap/GC/CPU data, peak RSS, failures, and history-sync counts.

Change only one workload dimension at a time. Recommended group sizes are 32, 128, 512, and 1024. Keep the Barback revision, rate, total, history size, container limits, host power state, and Docker version fixed across a baseline/candidate pair.
