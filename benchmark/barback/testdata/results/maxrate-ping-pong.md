<!--
Copyright (c) 2026 Rajeh Taher
Licensed under the MIT License. See LICENSE-MIT for details.
-->

# Ping-pong saturation comparison

Measured on 2026-08-07 with the Compose stack capped at 2 vCPU and 3.5 GB of service memory. Each run used a fresh PostgreSQL volume and device, 64 DM senders, 64 chat-affine workers, a 3-second Signal-session warmup, and a 10-second text-message flood. TLS, Noise verification, Signal encryption/decryption, PostgreSQL persistence, server acknowledgements, and Barback pong decryption remained enabled.

Revisions:

- HyperMeow: `fc8c62034115b587bd5102834ee57ed1bdbda997`
- WhatsMeow: `39b719baa629e9cfaee5e095459c12e4df1eb5e4`

The upstream tree received only the benchmark socket URL, Origin, and Noise certificate-authority injection required to connect to Barback.

## Highest healthy rate

Healthy means the client completed every send without failure or queue overflow, Barback completed the session, the post-warmup flood stayed at the configured rate, and average wire pong RTT remained below 100 ms.

| Metric | HyperMeow | WhatsMeow |
| --- | ---: | ---: |
| Offered ping-pong pairs/s | 1,700 | 900 |
| Actual flood pairs/s | 1,699.82 | 900.27 |
| Successfully decrypted pongs | 17,062 / 17,064 | 9,063 / 9,064 |
| Successful pairs/s | 1,699.62 | 900.17 |
| Total ping + pong messages/s | 3,399.24 | 1,800.34 |
| Average wire pong RTT | 28.84 ms | 36.92 ms |
| Maximum wire pong RTT | 106.43 ms | 114.12 ms |
| Client send p99 | 26.28 ms | 30.58 ms |
| Client send failures | 0 | 0 |
| Client queue overflows | 0 | 0 |
| Peak client RSS | 38,739,968 B | 38,162,432 B |
| PostgreSQL calls | 120,343 | 127,534 |

HyperMeow sustained 1.888 times the successful ping-pong rate. Both runs had a negligible first-chain Signal setup failure rate (`counter 0` after the warmup): 2 pongs for HyperMeow and 1 for WhatsMeow. No live client send failed, and no message was lost on the wire.

## Saturation boundary

At 1,800 offered pairs/s, HyperMeow completed every client send and delivered 1,750.84 flood pairs/s, but average wire pong RTT rose to 270.16 ms. At 1,000 offered pairs/s, WhatsMeow completed every client send, but Barback still had one unresolved pong when the client settlement window ended. These are saturation points, not recommended operating rates.

The retained JSON reports are:

- `maxrate-hypermeow-final-1700.json`
- `maxrate-hypermeow-final-1800.json`
- `maxrate-whatsmeow-final-900.json`
- `maxrate-whatsmeow-final-1000.json`
