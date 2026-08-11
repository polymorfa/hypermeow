<!--
Copyright (c) 2026 Rajeh Taher
Licensed under the MIT License. See LICENSE-MIT for details.
-->

# Group-128 memory profile

The unchanged baseline and HyperMeow ran the same secure workload with full-rate Go memory profiling: 128 additional group members, 200 messages, and four history-sync payloads containing 8,000 messages. Both runs completed without send failures or queue overflows.

| Metric | Baseline | HyperMeow | Change |
| --- | ---: | ---: | ---: |
| Cumulative profiled allocation | 792.81 MB | 894.60 MB | +101.79 MB |
| Allocated objects | 7,723,126 | 6,132,776 | -1,590,350 |
| Retained heap after GC | 3.04 MB | 3.13 MB | +0.09 MB |
| Peak process RSS | 72.49 MB | 69.71 MB | -2.77 MB |

The regression is allocation volume, not retained memory or object count. HyperMeow creates fewer objects and does not exhibit heap growth, but its multi-row session upsert makes pgx build larger contiguous buffers:

| Differential allocation site | Change |
| --- | ---: |
| `pgtype.encodePlanBytesCodecBinaryBytes.Encode` | +102.50 MB |
| `pgproto3.(*Bind).Encode` | +94.80 MB |
| `pgproto3.(*Frontend).Flush` | -25.78 MB |
| pgx context watcher and cancellation setup | -20.67 MB |
| Query row and argument machinery | about -25 MB |

`PutManySessions` accounts for 272.79 MB of HyperMeow's cumulative allocation. The baseline's per-row `PutSession` path accounts for 117.21 MB. The larger bind buffers add about 155.6 MB, while eliminating identity and per-query work offsets about 53.8 MB, producing the measured net increase.

Other workload-wide outliers shared by both revisions are:

- 72.48 MB in libsignal debug logging, even though its default `Debug` method emits nothing. Caller discovery and `fmt.Sprint` occur before the no-op logger is invoked.
- 58.21 MB copying session blobs on `GetManySessions`: pgx allocates the decoded bytes and `database/sql` clones them again.
- 42.96 MB serializing and deserializing Signal sessions as JSON.
- About 96 MB constructing SHA-256 and HMAC state during Signal encryption.
- 16.4 MB decoding the 8,000-message history-sync protobuf payloads.

The first optimization target is the PostgreSQL session write transport: preserve one transaction and low statement count without assembling a full duplicate bind payload in `database/sql` and pgx. The second is libsignal's unconditional debug-message formatting. Session read copies and JSON persistence are material but require more invasive compatibility work.

Raw artifacts:

- `baseline-memory-g128.json`
- `baseline-memory-g128.heap.pb.gz`
- `candidate-memory-g128.json`
- `candidate-memory-g128.heap.pb.gz`
