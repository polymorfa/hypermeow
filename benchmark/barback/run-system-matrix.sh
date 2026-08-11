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
export BENCH_TIMEOUT=${BENCH_TIMEOUT:-8m}
setup_attempts=${BENCH_SETUP_ATTEMPTS:-3}
if ! [[ $setup_attempts =~ ^[1-9][0-9]*$ ]]; then
	printf 'BENCH_SETUP_ATTEMPTS must be a positive integer\n' >&2
	exit 2
fi

cleanup() {
	docker compose -f compose.yaml -f "${compose_override:-compose.dm.yaml}" down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

run_scenario() {
	local scenario=$1
	local stats_file
	export BENCH_MESSAGE_PROFILE=text HISTORY_CONVERSATIONS=0 HISTORY_MESSAGES=0
	export BENCH_GROUP_SIZE=0 BENCH_MODE=dm BENCH_WORKERS=1
	compose_override=compose.dm.yaml

	case "$scenario" in
		dm-burst-16)
			export BENCH_RATE=400 BENCH_TOTAL=1600 BENCH_SENDERS=16 BENCH_WARMUP_MS=2000
			;;
		dm-history-8)
			export BENCH_RATE=80 BENCH_TOTAL=400 BENCH_SENDERS=8 BENCH_WARMUP_MS=3000
			export HISTORY_CONVERSATIONS=100 HISTORY_MESSAGES=20
			;;
		dm-mixed-8)
			export BENCH_RATE=40 BENCH_TOTAL=320 BENCH_SENDERS=8 BENCH_WARMUP_MS=2500
			export HISTORY_CONVERSATIONS=10 HISTORY_MESSAGES=5 BENCH_MESSAGE_PROFILE=mixed BENCH_WORKERS=4
			;;
		group-128)
			export BENCH_RATE=50 BENCH_TOTAL=400 BENCH_SENDERS=1 BENCH_WARMUP_MS=3000
			export BENCH_GROUP_SIZE=128 BENCH_MODE=group HISTORY_CONVERSATIONS=100 HISTORY_MESSAGES=20
			compose_override=compose.yaml
			;;
		group-mixed-32)
			export BENCH_RATE=25 BENCH_TOTAL=160 BENCH_SENDERS=1 BENCH_WARMUP_MS=2500
			export BENCH_GROUP_SIZE=32 BENCH_MODE=group HISTORY_CONVERSATIONS=10 HISTORY_MESSAGES=5 BENCH_MESSAGE_PROFILE=mixed BENCH_WORKERS=2
			compose_override=compose.yaml
			;;
		*)
			printf 'unknown benchmark scenario: %s\n' "$scenario" >&2
			return 2
			;;
	esac

	export BENCH_SCENARIO=$scenario
	export RESULT_FILE="${BENCH_VARIANT}-${scenario}.json"
	if [[ ${MEM_PROFILE_SCENARIO:-} == "$scenario" ]]; then
		export MEM_PROFILE_PATH="/results/${BENCH_VARIANT}-${scenario}.heap.pb.gz"
	else
		export MEM_PROFILE_PATH=
	fi
	stats_file="results/${BENCH_VARIANT}-${scenario}.docker-stats.ndjson"

	cleanup
	: > "$stats_file"
	printf 'Running %s (%s, %s)\n' "$scenario" "$BENCH_VARIANT" "$BUILD_REV"
	local compose=(docker compose -f compose.yaml -f "$compose_override")
	if [[ ${BENCH_LOCAL_IMAGE_CACHE:-0} == 1 ]]; then
		compose+=(-f compose.local-cache.yaml)
	fi
	local setup_attempt=1
	local up_args=(up --build --wait)
	if [[ ${BENCH_LOCAL_IMAGE_CACHE:-0} == 1 ]]; then
		up_args+=(--pull never)
	fi
	while ! "${compose[@]}" "${up_args[@]}"; do
		if ((setup_attempt >= setup_attempts)); then
			return 1
		fi
		printf 'Setup failed; retrying %s (%d/%d)\n' "$scenario" "$((setup_attempt + 1))" "$setup_attempts" >&2
		cleanup
		((setup_attempt++))
	done
	(
		while true; do
			local_ids=$("${compose[@]}" ps -q)
			if [[ -n $local_ids ]]; then
				docker stats --no-stream --format '{{json .}}' $local_ids >> "$stats_file" 2>/dev/null || true
			fi
			sleep 0.5
		done
	) &
	local sampler_pid=$!
	"${compose[@]}" wait client
	kill "$sampler_pid" >/dev/null 2>&1 || true
	wait "$sampler_pid" 2>/dev/null || true
}

if (($# == 0)); then
	set -- dm-burst-16 dm-history-8 dm-mixed-8 group-128 group-mixed-32
fi

for scenario in "$@"; do
	run_scenario "$scenario"
done
