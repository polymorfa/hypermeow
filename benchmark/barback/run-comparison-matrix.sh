#!/usr/bin/env bash
set -euo pipefail

benchmark_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$benchmark_dir/../.." && pwd)
temp_dir=$(mktemp -d)

cleanup() {
	rm -rf -- "$temp_dir"
}
trap cleanup EXIT

whatsmeow_ref=${WHATSMEOW_REF:-upstream/main}
pre_pr3_ref=${PRE_PR3_REF:-de92d3debb6287c7570e51cb8ff52c75408214b4}
whatsmeow_sha=$(git -C "$repo_dir" rev-parse "$whatsmeow_ref^{commit}")
pre_pr3_sha=$(git -C "$repo_dir" rev-parse "$pre_pr3_ref^{commit}")
candidate_sha=$(git -C "$repo_dir" rev-parse HEAD)
repeats=${BENCH_REPEATS:-1}
repeat_start=${BENCH_REPEAT_START:-1}
if ! [[ $repeats =~ ^[1-9][0-9]*$ ]]; then
	printf 'BENCH_REPEATS must be a positive integer\n' >&2
	exit 2
fi
if ! [[ $repeat_start =~ ^[1-9][0-9]*$ ]] || ((repeat_start > repeats)); then
	printf 'BENCH_REPEAT_START must be a positive integer no greater than BENCH_REPEATS\n' >&2
	exit 2
fi

mkdir -p "$temp_dir/whatsmeow" "$temp_dir/pre-pr3"
git -C "$repo_dir" archive "$whatsmeow_sha" | tar -x -C "$temp_dir/whatsmeow"
git -C "$repo_dir" archive "$pre_pr3_sha" | tar -x -C "$temp_dir/pre-pr3"
git -C "$temp_dir/whatsmeow" apply "$benchmark_dir/patches/barback-socket-config.patch"

cd "$benchmark_dir"
if (($# == 0)); then
	scenarios=(dm-burst-16 dm-history-8 dm-mixed-8 group-128 group-mixed-32)
else
	scenarios=("$@")
fi

run_revision() {
	local name=$1 context=$2 revision=$3 repeat=$4 scenario=$5 variant=$1
	if ((repeats > 1)); then
		variant="${name}-r${repeat}"
	fi
	LIBRARY_CONTEXT="$context" BUILD_REV="$revision" BENCH_VARIANT="$variant" ./run-system-matrix.sh "$scenario"
}

for ((repeat = repeat_start; repeat <= repeats; repeat++)); do
	for scenario in "${scenarios[@]}"; do
		run_revision whatsmeow "$temp_dir/whatsmeow" "$whatsmeow_sha" "$repeat" "$scenario"
		run_revision pre-pr3 "$temp_dir/pre-pr3" "$pre_pr3_sha" "$repeat" "$scenario"
		run_revision hypermeow "$repo_dir" "$candidate_sha" "$repeat" "$scenario"
	done
done
