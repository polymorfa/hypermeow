#!/usr/bin/env bash
set -euo pipefail

benchmark_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$benchmark_dir/../.." && pwd)
temp_dir=$(mktemp -d)
. "$benchmark_dir/comparison-matrix-env.sh"

cleanup() {
	rm -rf -- "$temp_dir"
}
trap cleanup EXIT

whatsmeow_ref=${WHATSMEOW_REF:-upstream/main}
pre_pr3_ref=${PRE_PR3_REF:-de92d3debb6287c7570e51cb8ff52c75408214b4}
candidate_ref=${CANDIDATE_REF:-HEAD}
whatsmeow_sha=$(git -C "$repo_dir" rev-parse "$whatsmeow_ref^{commit}")
pre_pr3_sha=$(git -C "$repo_dir" rev-parse "$pre_pr3_ref^{commit}")
candidate_sha=$(git -C "$repo_dir" rev-parse "$candidate_ref^{commit}")
candidate_build_tags=${CANDIDATE_BUILD_TAGS-}
if [[ ! -v CANDIDATE_BUILD_TAGS ]]; then
	required_candidate_symbols=(
		'func (cli *Client) GetCatalog('
		'func (cli *Client) GetCatalogProduct('
		'func (cli *Client) GetCatalogProducts('
		'func (cli *Client) GetProductCollection('
		'func (cli *Client) GetProductCollections('
		'func (cli *Client) GetOrderDetails('
	)
	if [[ -f "$benchmark_dir/cmd/bench/security_code_smoke.go" ]]; then
		required_candidate_symbols+=('func (cli *Client) GetIdentityVerificationCodes(')
	fi
	if [[ -f "$benchmark_dir/cmd/bench/phone_consent_sync.go" ]]; then
		required_candidate_symbols+=('func (int *DangerousInternalClient) SetSynchronousMessageNameUpdates(')
	fi
	for symbol in "${required_candidate_symbols[@]}"; do
		if ! git -C "$repo_dir" grep -Fq "$symbol" "$candidate_sha" -- '*.go'; then
			candidate_build_tags=benchmark_legacy
			break
		fi
	done
fi
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

mkdir -p "$temp_dir/whatsmeow" "$temp_dir/pre-pr3" "$temp_dir/candidate"
git -C "$repo_dir" archive "$whatsmeow_sha" | tar -x -C "$temp_dir/whatsmeow"
git -C "$repo_dir" archive "$pre_pr3_sha" | tar -x -C "$temp_dir/pre-pr3"
git -C "$repo_dir" archive "$candidate_sha" | tar -x -C "$temp_dir/candidate"
git -C "$temp_dir/whatsmeow" apply "$benchmark_dir/patches/barback-socket-config.patch"

cd "$benchmark_dir"
if (($# == 0)); then
	scenarios=(dm-burst-16 dm-history-8 dm-mixed-8 group-128 group-mixed-32)
else
	scenarios=("$@")
fi

run_revision() {
	local name=$1 context=$2 revision=$3 repeat=$4 scenario=$5 variant=$1
	local build_tags=
	if [[ $name != hypermeow ]]; then
		build_tags=benchmark_legacy
	else
		build_tags=$candidate_build_tags
	fi
	local security_code_smoke
	security_code_smoke=$(comparison_security_code_smoke "$name" "$build_tags" "${BENCH_SECURITY_CODE_SMOKE:-false}")
	if ((repeats > 1)); then
		variant="${name}-r${repeat}"
	fi
	if [[ $security_code_smoke == true ]]; then
		LIBRARY_CONTEXT="$context" BUILD_REV="$revision" BENCH_VARIANT="${variant}-security-smoke" BENCH_BUILD_TAGS="$build_tags" BENCH_SECURITY_CODE_SMOKE=true BENCH_SMOKE_ONLY=true MEM_PROFILE_SCENARIO= ./run-system-matrix.sh "$scenario"
	fi
	LIBRARY_CONTEXT="$context" BUILD_REV="$revision" BENCH_VARIANT="$variant" BENCH_BUILD_TAGS="$build_tags" BENCH_SECURITY_CODE_SMOKE=false BENCH_SMOKE_ONLY=false ./run-system-matrix.sh "$scenario"
}

for ((repeat = repeat_start; repeat <= repeats; repeat++)); do
	for scenario in "${scenarios[@]}"; do
		run_revision whatsmeow "$temp_dir/whatsmeow" "$whatsmeow_sha" "$repeat" "$scenario"
		run_revision pre-pr3 "$temp_dir/pre-pr3" "$pre_pr3_sha" "$repeat" "$scenario"
		run_revision hypermeow "$temp_dir/candidate" "$candidate_sha" "$repeat" "$scenario"
	done
done
