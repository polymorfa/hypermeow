# HyperMeow Barback benchmark

This benchmark pairs a real WhatsMeow client with Barback, stores its device and Signal state in PostgreSQL, and drives DM or group messages through the full Noise, Signal, receipt, sender-key, and history-sync paths.

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

The default Compose file runs the group workload. Add the DM overlay to run direct messages without weakening TLS or Noise verification:

```sh
docker compose -f compose.yaml -f compose.dm.yaml down --volumes --remove-orphans
BENCH_MODE=dm BENCH_SENDERS=8 docker compose -f compose.yaml -f compose.dm.yaml up --build --wait
docker compose -f compose.yaml -f compose.dm.yaml wait client
docker compose -f compose.yaml -f compose.dm.yaml down --volumes --remove-orphans
```

`run-dm-matrix.sh` starts every scenario with new certificates, a new device, and a new PostgreSQL volume. Its default matrix covers one repeatedly active peer, 8, 32, and 64 parallel peers, a 400-message-per-second burst, and DM traffic combined with 8,000 delivered history messages. Pass scenario names to run only a subset:

```sh
BENCH_VARIANT=candidate BUILD_REV="$(git rev-parse HEAD)" ./run-dm-matrix.sh
BENCH_VARIANT=candidate ./run-dm-matrix.sh dm-parallel-32 dm-burst-16
```

Set `LIBRARY_CONTEXT` to another worktree and change `BENCH_VARIANT` for an A/B run. Set `MEM_PROFILE_SCENARIO` to one scenario name to capture its full-rate heap profile. Each JSON report embeds the complete workload configuration.

`run-system-matrix.sh` adds group and mixed-feature workloads and captures Docker CPU, memory, network, block-I/O, process-I/O, temporary-file, and client network counters. Mixed runs rotate deterministically through text, links, mentions, quotes, forwarding, ephemeral settings, locations, contacts, polls, reactions, images, audio, documents, video, view-once media, and link previews. Documents and video use the streaming upload path so temporary-file behavior is measured.

Container setup is retried up to three times by default for transient registry or daemon failures. Set `BENCH_SETUP_ATTEMPTS=1` to disable setup retries; workload failures are never retried.

After one successful Barback build, set `BENCH_LOCAL_IMAGE_CACHE=1` to reuse that image and prohibit registry pulls during a repeated matrix. The client builder and runtime images are digest-pinned, so the cache-only mode still rebuilds the tested client binary for every library revision.

`run-comparison-matrix.sh` archives immutable WhatsMeow and pre-PR3 revisions, then runs them and the candidate through the same matrix. Set `BENCH_REPEATS=3` for repeated comparisons:

```sh
BENCH_REPEATS=3 ./run-comparison-matrix.sh
```

Resume an interrupted repeated matrix with `BENCH_REPEAT_START`, keeping `BENCH_REPEATS` set to the final repeat number.

Set `CANDIDATE_REF` to benchmark a frozen candidate while using newer harness code. The comparison driver selects the legacy build mode when that revision lacks APIs imported by newer smoke checks. Set `CANDIDATE_BUILD_TAGS` explicitly to override this detection for a custom library. All three libraries are exported to temporary immutable contexts before the matrix starts.

The archived upstream baseline receives only `patches/barback-socket-config.patch`, the same URL, Origin, and Noise certificate-authority injection already present before PR #3. The patch is required to connect upstream WhatsMeow to Barback and contains no runtime optimization.

The constructor benchmark measures fixed disconnected client state separately from live-session traffic:

```sh
BENCH_VARIANT=hypermeow BENCH_SESSIONS=2000 ./run-client-memory.sh
```

`BARBACK_CONTEXT` must point to a checkout of `titan-api/devcenter` at commit `9e7d0dc45faffc9fde6ab4f9fd405a3a61c0efe5`. The default resolves to the standard local Polymorfa checkout layout. Verify the commit before a comparison with `git -C "$BARBACK_CONTEXT" rev-parse HEAD`.

`LIBRARY_CONTEXT` can point to another HyperMeow worktree to compare two revisions without changing the benchmark code or branches. Barback generates a persisted TLS certificate for each clean stack. The client trusts that certificate and keeps both TLS and Noise certificate verification enabled.

Fresh results are written to the ignored `results/` workspace. PostgreSQL statement statistics are reset on the authenticated connection event, before Barback's benchmark warmup. The report includes the top WhatsMeow queries, total statement calls and execution time, send and upload latency percentiles, throughput, Go heap/GC/CPU data, peak RSS, process and block I/O, temporary-file peaks and cleanup, network traffic, failures, message-shape counts, and history-sync counts. `session_runtime` begins at the connected event and includes history sync; `workload_runtime` begins at the first live benchmark message. Docker stats include the whole container lifetime. Network timings are local-container transport measurements, not internet latency.

When `BENCH_WORKERS` is greater than one, messages are assigned to chat-affine worker queues. Different chats run concurrently, while messages for one Signal session remain ordered. Set `BARBACK_LOG_LEVEL=info` when validating ping-pong throughput so the final decrypted-pong count and wire RTT are visible in the Barback logs.

The frozen three-repeat comparison is summarized in `testdata/results/system-comparison.md`; its versioned JSON reports and Docker-stat streams remain alongside it.

Set `MEM_PROFILE_PATH=/results/run.heap.pb.gz` to capture a full-rate Go allocation profile. Profiling changes runtime cost, so compare profiles with each other rather than with ordinary benchmark timings. Inspect cumulative allocations with `go tool pprof -top -alloc_space results/run.heap.pb.gz` and retained heap with `go tool pprof -top -inuse_space results/run.heap.pb.gz`. Move only deliberately retained evidence into `testdata/results/`.

Change only one workload dimension at a time. Recommended group sizes are 32, 128, 512, and 1024. Keep the Barback revision, mode, sender count, rate, total, history size, container limits, host power state, and Docker version fixed across a baseline/candidate pair.

Set `BENCH_BUSINESS_SMOKE=true` to validate live catalog, product, collection, product-list, and order reads through the paired HyperMeow connection before accepting the benchmark result. The fixtures are generated by Barback and never contact a WhatsApp account.

Set `BENCH_PHONE_CONSENT_SMOKE=true BARBACK_CAPTURE_MESSAGES=10` to send a
request-phone-number message and a share-phone-number protocol message through
Signal, then verify both decrypted protobufs in Barback's bounded capture
buffer. This uses only the synthetic browser and fake phone identities.

Set `BENCH_SECURITY_CODE_SMOKE=true` on `run-comparison-matrix.sh` to validate
LID identity verification codes. The driver runs that check in a separate
smoke-only stack, removes its PostgreSQL volume, and then starts the measured
candidate from cold state. Direct Compose runs must also set
`BENCH_SMOKE_ONLY=true`; combining this validation with a measured workload is
rejected because it would warm the candidate's device and identity caches.
