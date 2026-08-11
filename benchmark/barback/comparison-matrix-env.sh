#!/usr/bin/env bash
# Copyright (c) 2026 Rajeh Taher
#
# Licensed under the MIT License. See LICENSE-MIT for details.


comparison_security_code_smoke() {
	local name=$1 build_tags=$2 requested=$3
	if [[ $name != hypermeow || $build_tags =~ (^|[[:space:],])benchmark_legacy($|[[:space:],]) ]]; then
		printf 'false\n'
	else
		printf '%s\n' "$requested"
	fi
}
