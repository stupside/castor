#!/usr/bin/env bash
# Render the Homebrew cask for this release and push it to the tap.
# Run from the repo root, in CI, after release.sh has staged the archives.
set -euo pipefail

version="${VERSION:?VERSION must be set}"

sha_arm=$(sha256sum "dist/castor_${version}_darwin_arm64.tar.gz" | cut -d' ' -f1)
sha_intel=$(sha256sum "dist/castor_${version}_darwin_amd64.tar.gz" | cut -d' ' -f1)
export VERSION="$version" SHA_ARM="$sha_arm" SHA_INTEL="$sha_intel"

tap=$(mktemp -d)
git clone --depth 1 "https://x-access-token:${TAP_TOKEN}@github.com/stupside/homebrew-tap.git" "$tap"
# Keep $VARS literal for envsubst; Ruby's #{...} in the template must survive.
# shellcheck disable=SC2016
envsubst '$VERSION $SHA_ARM $SHA_INTEL' < .github/homebrew-castor.rb.tmpl > "$tap/Casks/castor.rb"
git -C "$tap" add Casks/castor.rb
git -C "$tap" -c user.name=castor -c user.email=git@castor.dev \
  commit -m "Brew cask update for castor $version"
git -C "$tap" push
