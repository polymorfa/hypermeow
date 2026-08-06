#!/usr/bin/env bash
set -euo pipefail

benchmark_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$benchmark_dir/../.." && pwd)
library_context=${LIBRARY_CONTEXT:-$repo_dir}
build_rev=${BUILD_REV:-working-tree}
variant=${BENCH_VARIANT:-candidate}
sessions=${BENCH_SESSIONS:-2000}
image="hypermeow-clientmem:${variant}"

docker build \
	--build-context "library=$library_context" \
	--build-arg "BUILD_REV=$build_rev" \
	-f "$benchmark_dir/Dockerfile.clientmem" \
	-t "$image" \
	"$repo_dir"
docker run --rm --memory=512m --cpus=1 \
	-e "BENCH_SESSIONS=$sessions" \
	-e "RESULT_PATH=/results/${variant}-client-memory-${sessions}.json" \
	-v "$benchmark_dir/results:/results" \
	"$image"
