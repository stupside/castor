#!/usr/bin/env bash
# Stage the native binaries (from the build matrix, in prebuilt/) into release
# archives + checksums.txt + a Docker build context. Run from the repo root, in CI.
set -euo pipefail

version="${VERSION:?VERSION must be set}"

mkdir -p dist docker
for dir in prebuilt/castor-*; do
  target="${dir#prebuilt/castor-}"   # e.g. linux-amd64
  os="${target%-*}"; arch="${target##*-}"

  work=$(mktemp -d)
  cp "$dir/castor" "$work/castor"; chmod +x "$work/castor"
  cp LICENSE README.md "$work/"
  tar -C "$work" -czf "dist/castor_${version}_${os}_${arch}.tar.gz" castor LICENSE README.md

  if [ "$os" = linux ]; then
    mkdir -p "docker/$arch"
    cp "$dir/castor" "docker/$arch/castor"
  fi
done

( cd dist && sha256sum castor_*.tar.gz > checksums.txt )
