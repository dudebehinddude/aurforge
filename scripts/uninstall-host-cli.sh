#!/bin/sh
set -eu

bin_dir="${AURFORGE_CLI_BIN_DIR:-/host-bin}"
target="${bin_dir}/aurforge"

if [ -e "${target}" ] || [ -L "${target}" ]; then
	rm -f "${target}"
	printf 'aurforge: removed host CLI at %s\n' "${target}" >&2
fi
