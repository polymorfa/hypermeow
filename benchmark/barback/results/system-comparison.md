# HyperMeow system comparison

On 2026-08-06, 45 clean Barback workloads compared upstream WhatsMeow, the state before PR #3, and the frozen HyperMeow candidate. Every revision completed three repetitions of five scenarios. All 45 runs reached their message target with zero send failures, zero queue overflows, and zero temporary files remaining.

## Revisions and method

| Variant | Revision |
| --- | --- |
| WhatsMeow | `e9a033b2493356741a604a09e50c34b371882338` |
| Pre-PR3 | `de92d3debb6287c7570e51cb8ff52c75408214b4` |
| HyperMeow | `be55f11511f0da723aeb5ec301613572bbbac386` |
| Barback | `9e7d0dc45faffc9fde6ab4f9fd405a3a61c0efe5` |

The revisions were interleaved by repeat and scenario. PostgreSQL, Barback, and the client were recreated with a fresh PostgreSQL volume and device for every run. The Compose stack was limited to 2 vCPU and 3.5 GB: 0.5 CPU/768 MB for PostgreSQL, 0.5 CPU/768 MB for Barback, and 1 CPU/2 GB for the client. Builder and runtime images were digest-pinned, and the exact Barback image was reused after its first build.

Upstream WhatsMeow received only the Barback socket test hook needed to inject the local URL, Origin, and Noise certificate authority. That patch contains no performance or persistence change. TLS and Noise verification remained enabled.

Mixed workloads used an exact, deterministic distribution of 16 message shapes: short and rich text, URL text, quoted mentions, forwarded and ephemeral attributes, location, vCard contact, poll, reaction, image, PTT audio, PDF document, video, view-once image, link preview, spoiler/after-read attributes, and medium text. Each mixed DM run uploaded 39,321,600 bytes; each mixed group run uploaded 19,660,800 bytes. Documents and videos used `UploadReader` and its temporary-file path.

Values below are medians of three. Raw JSON and Docker samples are the `whatsmeow-r*`, `pre-pr3-r*`, and `hypermeow-r*` files in this directory.

## Result summary

Negative percentages are improvements. The comparison is HyperMeow relative to the named baseline.

| Scenario | Allocation vs WM / pre | RSS vs WM / pre | CPU vs WM / pre | Send p95 vs WM / pre | SQL calls vs WM / pre | Network bytes vs WM / pre |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| DM burst, 16 peers | -7.0% / -9.8% | -1.2% / -1.3% | -14.8% / -5.0% | -31.7% / -8.6% | -49.2% / 0.0% | -11.6% / -1.3% |
| DM and history, 8 peers | -3.4% / -10.7% | +3.8% / +3.2% | -11.5% / -4.9% | -19.2% / -16.7% | -74.3% / +0.0% | -23.9% / +1.4% |
| Mixed DM, 8 peers | -5.3% / -7.4% | +0.5% / -0.5% | -1.9% / +0.1% | -20.6% / -5.6% | -49.1% / 0.0% | -1.7% / -0.2% |
| Group, 128 members | -15.9% / -25.9% | +5.0% / -0.5% | -58.2% / -17.6% | -79.3% / -14.0% | -97.9% / 0.0% | -28.0% / -0.3% |
| Mixed group, 32 members | -14.2% / -18.4% | -6.6% / -1.6% | -20.7% / -11.7% | -59.6% / -22.5% | -92.2% / 0.0% | -13.1% / -0.3% |

The candidate improves measured allocation in all five scenarios relative to both baselines. History allocation uses the connected-to-completion `session_runtime` interval so all history sync work is inside the measurement; the first-live-message boundary was timing-sensitive. History peak RSS remains 3.2% above pre-PR3 and is the clearest next retained-memory target.

### CPU, memory, and PostgreSQL

| Scenario | Variant | Go allocation (MiB) | Peak RSS (MiB) | Client CPU (s) | GC | SQL calls | SQL time (ms) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| DM burst | WhatsMeow | 218.88 | 34.14 | 1.298 | 56 | 22,654 | 185.82 |
| DM burst | Pre-PR3 | 225.64 | 34.15 | 1.164 | 57 | 11,511 | 141.57 |
| DM burst | HyperMeow | 203.57 | 33.71 | 1.107 | 55 | 11,511 | 160.86 |
| DM + history | WhatsMeow | 99.03 | 59.89 | 0.803 | 19 | 11,790 | 274.51 |
| DM + history | Pre-PR3 | 107.13 | 60.21 | 0.748 | 20 | 3,025 | 155.62 |
| DM + history | HyperMeow | 95.70 | 62.16 | 0.711 | 18 | 3,026 | 138.56 |
| Mixed DM | WhatsMeow | 63.94 | 37.79 | 0.875 | 12 | 4,820 | 96.64 |
| Mixed DM | Pre-PR3 | 65.43 | 38.16 | 0.857 | 12 | 2,456 | 78.05 |
| Mixed DM | HyperMeow | 60.58 | 37.98 | 0.858 | 12 | 2,455 | 77.40 |
| Group 128 | WhatsMeow | 1,495.85 | 57.36 | 5.502 | 398 | 165,053 | 993.12 |
| Group 128 | Pre-PR3 | 1,697.03 | 60.54 | 2.792 | 401 | 3,456 | 421.42 |
| Group 128 | HyperMeow | 1,258.28 | 60.25 | 2.301 | 368 | 3,456 | 409.62 |
| Mixed group 32 | WhatsMeow | 175.17 | 38.44 | 1.076 | 35 | 17,693 | 130.08 |
| Mixed group 32 | Pre-PR3 | 184.09 | 36.48 | 0.967 | 41 | 1,377 | 100.33 |
| Mixed group 32 | HyperMeow | 150.30 | 35.91 | 0.853 | 36 | 1,377 | 106.72 |

SQL execution time is host- and page-cache-sensitive. HyperMeow is lower than pre-PR3 in mixed DM and group 128, but is 6.4% higher in mixed group and 13.6% higher in the DM burst. The burst difference is 19.3 ms over 11,511 calls and did not increase client CPU or latency.

### Latency and throughput

| Scenario | Variant | Send p50 (ms) | Send p95 (ms) | Send p99 (ms) | Upload p95 (ms) | Duration (s) | Throughput (/s) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| DM burst | WhatsMeow | 1.100 | 2.017 | 3.365 | — | 5.923 | 270.14 |
| DM burst | Pre-PR3 | 0.803 | 1.506 | 2.360 | — | 5.915 | 270.51 |
| DM burst | HyperMeow | 0.732 | 1.377 | 2.315 | — | 5.916 | 270.47 |
| DM + history | WhatsMeow | 1.808 | 3.787 | 6.630 | — | 7.797 | 51.30 |
| DM + history | Pre-PR3 | 1.322 | 3.674 | 5.121 | — | 7.775 | 51.45 |
| DM + history | HyperMeow | 1.208 | 3.061 | 5.690 | — | 7.793 | 51.33 |
| Mixed DM | WhatsMeow | 5.974 | 10.126 | 13.260 | 9.859 | 10.107 | 31.66 |
| Mixed DM | Pre-PR3 | 4.015 | 8.516 | 10.559 | 9.188 | 10.099 | 31.69 |
| Mixed DM | HyperMeow | 3.465 | 8.037 | 11.732 | 11.218 | 10.108 | 31.66 |
| Group 128 | WhatsMeow | 29.656 | 33.998 | 44.160 | — | 15.043 | 26.59 |
| Group 128 | Pre-PR3 | 6.288 | 8.168 | 13.798 | — | 10.935 | 36.58 |
| Group 128 | HyperMeow | 5.103 | 7.024 | 11.508 | — | 10.942 | 36.56 |
| Mixed group 32 | WhatsMeow | 12.107 | 14.491 | 22.333 | 3.766 | 8.812 | 18.16 |
| Mixed group 32 | Pre-PR3 | 4.218 | 7.558 | 9.842 | 6.192 | 8.807 | 18.17 |
| Mixed group 32 | HyperMeow | 3.640 | 5.860 | 7.962 | 5.645 | 8.809 | 18.16 |

Input rates cap throughput, so send and upload percentiles are the meaningful speed measures. Local-container latency does not represent WhatsApp WAN latency. The Barback CA requires a benchmark-specific HTTP client, so the shared default transport optimization is covered by isolation tests but not by this transport timing.

### Network and I/O

| Scenario | Variant | RX (MiB) | TX (MiB) | Read chars (MiB) | Write chars (MiB) | Read/write syscalls | Physical writes (MiB) | Temp peak |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| DM burst | WhatsMeow | 16.30 | 18.91 | 13.94 | 16.67 | 59,821 / 34,569 | 0 | 0 |
| DM burst | Pre-PR3 | 15.02 | 16.49 | 13.45 | 14.95 | 37,205 / 22,001 | 0 | 0 |
| DM burst | HyperMeow | 14.76 | 16.35 | 13.39 | 15.01 | 29,911 / 17,874 | 0 | 0 |
| DM + history | WhatsMeow | 4.75 | 6.52 | 3.77 | 5.57 | 27,199 / 15,166 | 0 | 0 |
| DM + history | Pre-PR3 | 3.76 | 4.69 | 3.36 | 4.29 | 9,935 / 5,938 | 0 | 0 |
| DM + history | HyperMeow | 3.86 | 4.73 | 3.50 | 4.37 | 8,191 / 4,874 | 0 | 0 |
| Mixed DM | WhatsMeow | 3.40 | 41.81 | 27.82 | 66.09 | 14,501 / 13,329 | 25.16 | 1.25 MiB / 2 files |
| Mixed DM | Pre-PR3 | 3.15 | 41.36 | 27.72 | 65.75 | 9,898 / 10,690 | 25.16 | 1.25 MiB / 2 files |
| Mixed DM | HyperMeow | 3.10 | 41.34 | 27.71 | 65.76 | 8,452 / 9,804 | 25.16 | 1.25 MiB / 2 files |
| Group 128 | WhatsMeow | 72.88 | 106.47 | 61.97 | 95.55 | 358,588 / 191,689 | 0 | 0 |
| Group 128 | Pre-PR3 | 56.54 | 73.02 | 55.63 | 72.04 | 21,195 / 9,960 | 0 | 0 |
| Group 128 | HyperMeow | 56.64 | 72.50 | 55.77 | 71.82 | 17,087 / 8,944 | 0 | 0 |
| Mixed group 32 | WhatsMeow | 8.35 | 30.72 | 19.56 | 41.87 | 41,394 / 24,700 | 12.58 | sampler missed |
| Mixed group 32 | Pre-PR3 | 6.69 | 27.34 | 18.91 | 39.48 | 6,887 / 5,965 | 12.58 | 0.75 MiB / 1 file |
| Mixed group 32 | HyperMeow | 6.66 | 27.28 | 18.90 | 39.46 | 5,926 / 5,560 | 12.58 | 0.75 MiB / 1 file |

All mixed runs ended with zero temporary files and zero temporary bytes. The 10 ms sampler can miss a shorter-lived file, as it did in the upstream mixed-group median; process physical-write counters still measured the expected 12.58 MiB. Client cgroup block I/O remained zero because writes were absorbed by the page cache.

### Candidate Docker service peaks

Docker CPU values are maximum sampled percentages; memory and PostgreSQL block writes are median run maxima.

| Scenario | Client CPU / memory | PostgreSQL CPU / memory / writes | Barback CPU / memory |
| --- | ---: | ---: | ---: |
| DM burst | 26.05% / 18.69 MiB | 14.41% / 51.63 MiB / 107.77 MiB | 6.68% / 4.22 MiB |
| DM + history | 11.40% / 44.62 MiB | 7.70% / 46.40 MiB / 67.33 MiB | 3.42% / 7.41 MiB |
| Mixed DM | 10.16% / 22.41 MiB | 5.48% / 45.41 MiB / 64.95 MiB | 3.49% / 5.29 MiB |
| Group 128 | 27.64% / 45.81 MiB | 8.14% / 58.35 MiB / 128.75 MiB | 2.81% / 13.21 MiB |
| Mixed group 32 | 13.13% / 21.51 MiB | 6.47% / 42.26 MiB / 64.47 MiB | 3.95% / 7.07 MiB |

## Fixed client state

The separate constructor benchmark creates 2,000 disconnected clients under the same client memory limit.

| Variant | Heap | Heap per client | Heap in use | Peak RSS |
| --- | ---: | ---: | ---: | ---: |
| WhatsMeow | 157,858,312 B | 78,929 B | 158,040,064 B | 173,465,600 B |
| Pre-PR3 | 157,858,280 B | 78,929 B | 158,081,024 B | 175,431,680 B |
| HyperMeow | 8,480,848 B | 4,240 B | 8,585,216 B | 22,831,104 B |

HyperMeow saves 149,377,464 bytes of Go heap, or 142.46 MiB and 94.6%, versus upstream at 2,000 clients. This is fixed disconnected state, not a claim that 2,000 active WhatsApp sessions fit in 4 GB; active session memory depends on contacts, groups, media, event handlers, and application queues.

## Reliability and Go audit

- `go test -race ./...`, `go vet ./...`, and latest Staticcheck pass for the library. The benchmark module also passes race tests and vet.
- Latest govulncheck reports zero reachable vulnerabilities. One required module contains a reported vulnerable symbol that HyperMeow does not call.
- API diff reports no exported-API change between pre-PR3 and the candidate. Relative to upstream, the intentional Polymorfa Signal fork changes exported Signal parameter types; all other detected changes are additions.
- All direct dependencies are at their newest available versions. Nine indirect updates are available; none was mixed into this performance change without a demonstrated need.
- Unsupported Signal-store operations now return errors instead of panicking. Receipt misuse, CBC invariant failures, nil signed-prekey records, upload read failures, partial writes, and temporary-file cleanup have explicit failure paths.
- The node handler queue is bounded. Overflow forces one reconnect through an atomic guard rather than blocking the socket reader, dropping silently, or spawning unbounded goroutines.
- Attacker- and workload-influenced caches have limits; retry storage grows lazily and updates duplicate keys in place.
- PostgreSQL session batches use pgx-native typed arrays. Generic `database/sql` drivers retain the existing values path instead of paying the allocation cost of a driver wrapper intended for another driver.
- HTTP response bodies are bounded-drained and closed for connection reuse. The default transport is shared across clients, while proxy and custom-client setters preserve per-client isolation.
- The CodeRabbit CLI authenticated but did not emit a completed review after two 0.7.2 retries, so it is not counted as an audit pass. Manual review found and corrected the original blocking handler-queue overflow design before the final candidate was frozen.

One candidate mixed-DM repetition was a host/PostgreSQL outlier: 84.65 seconds and 169.07 ms send p95 versus 10.11 seconds and 7.28–8.04 ms in the other two repetitions. Its SQL execution time was 1,157.71 ms versus 71.37–77.40 ms. The sample remains in the raw results; the predeclared median prevents it from being silently discarded or dominating the summary.

The remaining demonstrated weak spot is history-heavy DM peak RSS despite its lower phase-stable allocation. WAN behavior, long-lived reconnect churn, proxy transports, actual WhatsApp throttling, and thousands of simultaneously active sessions require a staged canary on the target VPS before production capacity limits are changed.
