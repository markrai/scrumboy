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
#
# The archives are reproducible: building the same commit again gives the same
# bytes and checksums, so a re-run of the release workflow can tell "already
# published" from "something changed". Entry times come from SOURCE_DATE_EPOCH,
# defaulting to the commit time of HEAD. Needs GNU tar and coreutils (as on
# Linux) and Info-ZIP zip.
set -euo pipefail

version="${1:?usage: build-cli-release.sh <version> <outdir>}"
outdir="${2:?usage: build-cli-release.sh <version> <outdir>}"

if ! [[ "$version" =~ ^[A-Za-z0-9._+-]+$ ]]; then
  echo "refusing odd version string: $version" >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

epoch="${SOURCE_DATE_EPOCH:-$(git -C "$root" log -1 --format=%ct)}"
if ! [[ "$epoch" =~ ^[0-9]+$ ]]; then
  echo "cannot determine SOURCE_DATE_EPOCH (got: $epoch)" >&2
  exit 1
fi
tar=tar
tar --version 2>/dev/null | grep -q 'GNU tar' || tar=gtar
if ! "$tar" --version 2>/dev/null | grep -q 'GNU tar'; then
  echo "GNU tar is required for reproducible archives (macOS: brew install gnu-tar)" >&2
  exit 1
fi
command -v zip >/dev/null || { echo "zip is required" >&2; exit 1; }

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

  # Fixed modes, times, owners and entry order: nothing from the build host or
  # the clock ends up in the archive.
  chmod 0755 "$dir" "$dir/$bin"
  chmod 0644 "$dir/LICENSE"
  if [ "$goos" = "windows" ]; then
    # zip stores local time; -X drops uid/gid and extra timestamps, -D the
    # directory entry.
    TZ=UTC0 touch -d "@$epoch" "$dir/$bin" "$dir/LICENSE"
    (cd "$stage" && TZ=UTC0 zip -q -X -D "$outdir/$name.zip" "$name/LICENSE" "$name/$bin")
  else
    "$tar" -C "$stage" --sort=name --format=gnu --mtime="@$epoch" \
      --owner=0 --group=0 --numeric-owner -cf - "$name" | gzip -n > "$outdir/$name.tar.gz"
  fi
done

(
  cd "$outdir"
  sha256sum scrumboy-cli_"${version}"_*.tar.gz scrumboy-cli_"${version}"_*.zip \
    > "scrumboy-cli_${version}_checksums.txt"
)
echo "built ${#targets[@]} archives in $outdir"
