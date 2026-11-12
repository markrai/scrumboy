#!/usr/bin/env bash
# Cross-compile scrumboy-cli and package it for a release.
#
#   scripts/build-cli-release.sh <version> <outdir>
#
# Produces, in <outdir>:
#   scrumboy-cli_<version>_<os>_<arch>.tar.gz   (zip on windows)
#   scrumboy-cli_<version>_checksums.txt        (sha256, `sha256sum -c` format)
#
# <version> is the release tag (e.g. v3.35.0). The CLI has no cgo or sqlite
# dependency, so every target builds from a single host.
set -euo pipefail

version="${1:?usage: build-cli-release.sh <version> <outdir>}"
outdir="${2:?usage: build-cli-release.sh <version> <outdir>}"

if ! [[ "$version" =~ ^[A-Za-z0-9._+-]+$ ]]; then
  echo "refusing odd version string: $version" >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$outdir"
outdir="$(cd "$outdir" && pwd)"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

targets=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)

for target in "${targets[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="scrumboy-cli_${version}_${goos}_${goarch}"
  bin="scrumboy-cli"
  [ "$goos" = "windows" ] && bin="scrumboy-cli.exe"

  dir="$stage/$name"
  mkdir -p "$dir"
  (
    cd "$root"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -ldflags "-s -w -X main.version=${version}" \
      -o "$dir/$bin" ./cmd/scrumboy-cli
  )
  cp "$root/LICENSE" "$dir/LICENSE"

  if [ "$goos" = "windows" ]; then
    (cd "$stage" && zip -qr "$outdir/$name.zip" "$name")
  else
    tar -C "$stage" -czf "$outdir/$name.tar.gz" "$name"
  fi
done

(
  cd "$outdir"
  sha256sum scrumboy-cli_"${version}"_*.tar.gz scrumboy-cli_"${version}"_*.zip \
    > "scrumboy-cli_${version}_checksums.txt"
)
echo "built ${#targets[@]} archives in $outdir"
