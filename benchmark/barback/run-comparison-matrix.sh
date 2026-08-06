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
if ! [[ $repeats =~ ^[1-9][0-9]*$ ]]; then
	printf 'BENCH_REPEATS must be a positive integer\n' >&2
	exit 2
fi

mkdir -p "$temp_dir/whatsmeow" "$temp_dir/pre-pr3"
git -C "$repo_dir" archive "$whatsmeow_sha" | tar -x -C "$temp_dir/whatsmeow"
git -C "$repo_dir" archive "$pre_pr3_sha" | tar -x -C "$temp_dir/pre-pr3"

cd "$benchmark_dir"
run_revision() {
	local name=$1 context=$2 revision=$3 repeat variant
	shift 3
	for ((repeat = 1; repeat <= repeats; repeat++)); do
		variant=$name
		if ((repeats > 1)); then
			variant="${name}-r${repeat}"
		fi
		LIBRARY_CONTEXT="$context" BUILD_REV="$revision" BENCH_VARIANT="$variant" ./run-system-matrix.sh "$@"
	done
}

run_revision whatsmeow "$temp_dir/whatsmeow" "$whatsmeow_sha" "$@"
run_revision pre-pr3 "$temp_dir/pre-pr3" "$pre_pr3_sha" "$@"
run_revision hypermeow "$repo_dir" "$candidate_sha" "$@"
