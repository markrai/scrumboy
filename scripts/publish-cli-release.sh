#!/usr/bin/env bash
# Attach the scrumboy-cli release assets in <dist> to the GitHub Release <tag>.
#
#   scripts/publish-cli-release.sh <tag> <dist>
#
# Used by .github/workflows/cli-release.yml after every smoke test has passed,
# for both the release event and a manual dispatch. It never replaces an asset:
#
#   - an asset the release already has with the same sha256 is left alone, so
#     re-running the workflow for a published tag is a no-op (the archives are
#     reproducible, see build-cli-release.sh);
#   - an asset the release has with different bytes fails the run before
#     anything is uploaded. Replacing published files is a maintainer decision:
#     delete them on the release first, then re-run;
#   - missing assets are uploaded, unless the release is immutable (GitHub then
#     only accepts assets while the release is a draft), which fails the run.
#
# Prints formula=true|false (also to $GITHUB_OUTPUT when set): the Homebrew
# formula may only point at a published, non-prerelease release.
set -euo pipefail

tag="${1:?usage: publish-cli-release.sh <tag> <dist>}"
dist="${2:?usage: publish-cli-release.sh <tag> <dist>}"

shopt -s nullglob
assets=("$dist"/scrumboy-cli_"$tag"_*.tar.gz "$dist"/scrumboy-cli_"$tag"_*.zip)
if [ "${#assets[@]}" -eq 0 ]; then
  echo "no scrumboy-cli archives for $tag in $dist" >&2
  exit 1
fi
assets+=("$dist/scrumboy-cli_${tag}_checksums.txt" "$dist/scrumboy-cli.rb")
for f in "${assets[@]}"; do
  [ -s "$f" ] || { echo "missing release asset: $f" >&2; exit 1; }
done
# Never publish archives that differ from what the smoke tests verified.
(cd "$dist" && sha256sum --quiet -c "scrumboy-cli_${tag}_checksums.txt")

if ! release="$(gh release view "$tag" --json isDraft,isPrerelease,isImmutable,assets \
  --jq '{draft: .isDraft, prerelease: .isPrerelease, immutable: .isImmutable,
         assets: [.assets[] | {name, digest: (.digest // "")}]}')"; then
  echo "no GitHub Release for $tag: create it first; this workflow only attaches assets" >&2
  exit 1
fi
field() { jq -r "$1" <<<"$release"; }
draft="$(field .draft)"
prerelease="$(field .prerelease)"
immutable="$(field .immutable)"
echo "release $tag: draft=$draft prerelease=$prerelease immutable=$immutable"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

missing=() conflicts=()
for f in "${assets[@]}"; do
  name="$(basename "$f")"
  local_sum="$(sha256sum "$f" | cut -d' ' -f1)"
  if ! jq -e --arg n "$name" 'any(.assets[]; .name == $n)' <<<"$release" >/dev/null; then
    missing+=("$f")
    continue
  fi
  remote_sum="$(jq -r --arg n "$name" '.assets[] | select(.name == $n) | .digest' <<<"$release")"
  remote_sum="${remote_sum#sha256:}"
  if [ -z "$remote_sum" ]; then
    # Assets uploaded before GitHub recorded digests: hash the published bytes.
    gh release download "$tag" --pattern "$name" --dir "$work"
    remote_sum="$(sha256sum "$work/$name" | cut -d' ' -f1)"
  fi
  if [ "$remote_sum" = "$local_sum" ]; then
    echo "already published, identical: $name"
  else
    conflicts+=("$name")
  fi
done

if [ "${#conflicts[@]}" -gt 0 ]; then
  echo "release $tag already has different bytes for: ${conflicts[*]}" >&2
  echo "Nothing was uploaded. Published assets are never replaced automatically;" >&2
  echo "delete them on the release first if replacing them is intended." >&2
  exit 1
fi

if [ "${#missing[@]}" -gt 0 ]; then
  if [ "$immutable" = "true" ]; then
    echo "release $tag is immutable and lacks: ${missing[*]##*/}" >&2
    echo "GitHub only accepts assets on an immutable release while it is a draft." >&2
    exit 1
  fi
  # No --clobber: if an asset appears meanwhile, the upload fails instead of replacing it.
  gh release upload "$tag" "${missing[@]}"
  echo "uploaded: ${missing[*]##*/}"
else
  echo "nothing to upload"
fi

formula=false
[ "$draft" = "false" ] && [ "$prerelease" = "false" ] && formula=true
echo "formula=$formula"
if [ -n "${GITHUB_OUTPUT:-}" ]; then echo "formula=$formula" >> "$GITHUB_OUTPUT"; fi
