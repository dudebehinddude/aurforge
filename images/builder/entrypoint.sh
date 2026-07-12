#!/usr/bin/env bash
set -euo pipefail

# Disposable build layout:
#   /input  - read-only source snapshot from the host
#   /build  - writable workspace owned by the builder user
#   /output - host bind for finished packages
#
# Copy into a subdirectory (not onto /build itself) so cp never needs to
# change timestamps on a mount point or image directory we don't own.

workdir=/build/src
mkdir -p "$workdir" /output

if [[ ! -d /input ]]; then
  printf '%s\n' 'missing read-only source mount at /input' >&2
  exit 1
fi

cp -a /input/. "$workdir"/
if [[ ! -f "$workdir/PKGBUILD" ]]; then
  printf '%s\n' "PKGBUILD missing after copy from /input; contents:" >&2
  ls -la /input >&2 || true
  ls -la "$workdir" >&2 || true
  exit 1
fi
cd "$workdir"

if [[ "$#" -eq 0 ]]; then
  set -- makepkg --syncdeps --noconfirm --cleanbuild
fi

"$@"

shopt -s nullglob
packages=("$workdir"/*.pkg.tar.*)
if [[ "${#packages[@]}" -eq 0 ]]; then
  printf '%s\n' 'no package artifacts were produced in /build/src' >&2
  exit 1
fi

cp --preserve=mode,timestamps "${packages[@]}" /output/
