#!/usr/bin/env bash
set -euo pipefail

output_dir=/output
mkdir -p "$output_dir"

if [[ "$#" -eq 0 ]]; then
  set -- makepkg --syncdeps --noconfirm --cleanbuild
fi

"$@"

shopt -s nullglob
packages=(/build/*.pkg.tar.*)
if [[ "${#packages[@]}" -eq 0 ]]; then
  printf '%s\n' 'no package artifacts were produced in /build' >&2
  exit 1
fi

cp --preserve=mode,timestamps "${packages[@]}" "$output_dir/"
