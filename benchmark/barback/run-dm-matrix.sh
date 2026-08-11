#!/usr/bin/env bash
# Copyright (c) 2026 Rajeh Taher
#
# Licensed under the MIT License. See LICENSE-MIT for details.

set -euo pipefail

benchmark_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$benchmark_dir"

export BARBACK_CONTEXT=${BARBACK_CONTEXT:-../../../Titan/devcenter}
export LIBRARY_CONTEXT=${LIBRARY_CONTEXT:-../..}
export BUILD_REV=${BUILD_REV:-working-tree}
export BENCH_VARIANT=${BENCH_VARIANT:-candidate}
export BENCH_TIMEOUT=${BENCH_TIMEOUT:-5m}

compose=(docker compose -f compose.yaml -f compose.dm.yaml)

cleanup() {
	"${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

run_scenario() {
	local scenario=$1
	export BENCH_MESSAGE_PROFILE=text BENCH_WORKERS=1
	case "$scenario" in
		dm-steady-1)
			export BENCH_RATE=60 BENCH_TOTAL=600 BENCH_SENDERS=1 BENCH_WARMUP_MS=1500
			export HISTORY_CONVERSATIONS=0 HISTORY_MESSAGES=0
			;;
		dm-parallel-8)
			export BENCH_RATE=100 BENCH_TOTAL=800 BENCH_SENDERS=8 BENCH_WARMUP_MS=2000
			export HISTORY_CONVERSATIONS=0 HISTORY_MESSAGES=0
			;;
		dm-parallel-32)
			export BENCH_RATE=120 BENCH_TOTAL=960 BENCH_SENDERS=32 BENCH_WARMUP_MS=2500
			export HISTORY_CONVERSATIONS=0 HISTORY_MESSAGES=0
			;;
		dm-parallel-64)
			export BENCH_RATE=160 BENCH_TOTAL=1280 BENCH_SENDERS=64 BENCH_WARMUP_MS=3000
			export HISTORY_CONVERSATIONS=0 HISTORY_MESSAGES=0
			;;
		dm-burst-16)
			export BENCH_RATE=400 BENCH_TOTAL=1600 BENCH_SENDERS=16 BENCH_WARMUP_MS=2000
			export HISTORY_CONVERSATIONS=0 HISTORY_MESSAGES=0
			;;
		dm-history-8)
			export BENCH_RATE=80 BENCH_TOTAL=400 BENCH_SENDERS=8 BENCH_WARMUP_MS=3000
			export HISTORY_CONVERSATIONS=100 HISTORY_MESSAGES=20
			;;
		dm-mixed-8)
			export BENCH_RATE=40 BENCH_TOTAL=320 BENCH_SENDERS=8 BENCH_WARMUP_MS=2500
			export HISTORY_CONVERSATIONS=10 HISTORY_MESSAGES=5 BENCH_MESSAGE_PROFILE=mixed BENCH_WORKERS=4
			;;
		*)
			printf 'unknown DM scenario: %s\n' "$scenario" >&2
			return 2
			;;
	esac

	export BENCH_SCENARIO=$scenario BENCH_MODE=dm BENCH_GROUP_SIZE=0
	export RESULT_FILE="${BENCH_VARIANT}-${scenario}.json"
	if [[ ${MEM_PROFILE_SCENARIO:-} == "$scenario" ]]; then
		export MEM_PROFILE_PATH="/results/${BENCH_VARIANT}-${scenario}.heap.pb.gz"
	else
		export MEM_PROFILE_PATH=
	fi

	cleanup
	printf 'Running %s (%s)\n' "$scenario" "$BENCH_VARIANT"
	"${compose[@]}" up --build --wait
	"${compose[@]}" wait client
}

if (($# == 0)); then
		set -- dm-steady-1 dm-parallel-8 dm-parallel-32 dm-parallel-64 dm-burst-16 dm-history-8 dm-mixed-8
fi

for scenario in "$@"; do
	run_scenario "$scenario"
done
