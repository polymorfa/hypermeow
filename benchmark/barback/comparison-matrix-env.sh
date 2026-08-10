#!/usr/bin/env bash

comparison_security_code_smoke() {
	local name=$1 build_tags=$2 requested=$3
	if [[ $name != hypermeow || $build_tags =~ (^|[[:space:],])benchmark_legacy($|[[:space:],]) ]]; then
		printf 'false\n'
	else
		printf '%s\n' "$requested"
	fi
}
