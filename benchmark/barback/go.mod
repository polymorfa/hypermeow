// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

module github.com/polymorfa/hypermeow/benchmark/barback

go 1.25.0

toolchain go1.26.5

require (
	github.com/jackc/pgx/v5 v5.10.0
	github.com/polymorfa/hypermeow v0.0.0
	google.golang.org/protobuf v1.36.11
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/petermattis/goid v0.0.0-20260713124913-97594f28f5ca // indirect
	github.com/polymorfa/libsignal-protocol-go v0.2.3-0.20260806162910-a2adef2e8a11 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	go.mau.fi/util v0.9.12-0.20260717235539-f9ffa7eca58d // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/exp v0.0.0-20260709172345-9ea1abe57597 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

replace github.com/polymorfa/hypermeow => ../..
