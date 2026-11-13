#!/usr/bin/env bash
# Regression test for scripts/publish-cli-release.sh against a stub `gh`.
#
#   bash scripts/test-publish-cli-release.sh
#
# Needs bash, jq and sha256sum. Talks to nothing; everything lives in a temp dir.
set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/publish-cli-release.sh"
workflow="$root/.github/workflows/cli-release.yml"
tag="v9.9.9"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat > "$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  "release view")
    [ -f "$STATE/release.json" ] || { echo "release not found" >&2; exit 1; }
    shift 3; expr=.
    while [ $# -gt 0 ]; do case "$1" in --jq) expr="$2"; shift 2;; *) shift;; esac; done
    jq -c "$expr" "$STATE/release.json" ;;
  "release upload")
    shift 3
    for f in "$@"; do
      case "$f" in --clobber) echo "--clobber used" >&2; exit 1;; esac
      n="$(basename "$f")"
      if jq -e --arg n "$n" 'any(.assets[]; .name == $n)' "$STATE/release.json" >/dev/null; then
        echo "asset $n already exists" >&2; exit 1
      fi
      echo "$n" >> "$STATE/uploads"
    done ;;
  "release download")
    shift 3
    while [ $# -gt 0 ]; do case "$1" in --pattern) pat="$2"; shift 2;; --dir) dir="$2"; shift 2;; *) shift;; esac; done
    cp "$STATE/remote/$pat" "$dir/$pat" ;;
  *) echo "unexpected gh call: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "$work/bin/gh"

dist="$work/dist"
mkdir -p "$dist"
for t in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do echo "tar $t" > "$dist/scrumboy-cli_${tag}_$t.tar.gz"; done
for t in windows_amd64 windows_arm64; do echo "zip $t" > "$dist/scrumboy-cli_${tag}_$t.zip"; done
(cd "$dist" && sha256sum scrumboy-cli_"${tag}"_*.tar.gz scrumboy-cli_"${tag}"_*.zip > "scrumboy-cli_${tag}_checksums.txt")
echo 'class ScrumboyCli < Formula; end' > "$dist/scrumboy-cli.rb"
all=()
for f in "$dist"/*; do all+=("$(basename "$f")"); done
cp -r "$dist" "$work/dist.pristine"

# release <case> <draft> <prerelease> <immutable> [name=same|other|legacy-same|legacy-other ...]
release() {
  local st="$work/state/$1" draft="$2" pre="$3" imm="$4" assets="[]" spec n kind digest
  shift 4
  mkdir -p "$st/remote"
  for spec in "$@"; do
    n="${spec%%=*}" kind="${spec#*=}" digest=""
    case "$kind" in
      same) digest="sha256:$(sha256sum "$dist/$n" | cut -d' ' -f1)" ;;
      other) digest="sha256:$(printf other | sha256sum | cut -d' ' -f1)" ;;
      legacy-same) cp "$dist/$n" "$st/remote/$n" ;;
      legacy-other) echo other > "$st/remote/$n" ;;
    esac
    assets="$(jq -c --arg n "$n" --arg d "$digest" '. + [{name: $n, digest: (if $d == "" then null else $d end)}]' <<<"$assets")"
  done
  jq -n --argjson a "$assets" --argjson d "$draft" --argjson p "$pre" --argjson i "$imm" \
    '{isDraft: $d, isPrerelease: $p, isImmutable: $i, assets: $a}' > "$st/release.json"
}

failures=0
# expect <case> <exit> <uploaded-count> <formula: true|false|->
expect() {
  local st="$work/state/$1" out code uploads formula
  mkdir -p "$st"
  out="$(PATH="$work/bin:$PATH" STATE="$st" GITHUB_OUTPUT="$st/output" bash "$script" "$tag" "$dist" 2>&1)"
  code=$?
  uploads=0
  [ -f "$st/uploads" ] && uploads="$(wc -l < "$st/uploads" | tr -d ' ')"
  formula="$(sed -n 's/^formula=//p' "$st/output" 2>/dev/null)"
  if [ "$code" = "$2" ] && [ "$uploads" = "$3" ] && [ "${formula:--}" = "$4" ]; then
    echo "ok   $1"
  else
    echo "FAIL $1: exit=$code (want $2) uploads=$uploads (want $3) formula=${formula:--} (want $4)"
    sed 's/^/     | /' <<<"$out"
    failures=$((failures + 1))
  fi
}

identical=()
for n in "${all[@]}"; do identical+=("$n=same"); done

expect missing-release 1 0 -
release first-publication false false false;                          expect first-publication 0 8 true
release identical-rerun false false false "${identical[@]}";          expect identical-rerun 0 0 true
release partial false false false "${identical[@]:0:3}";             expect partial 0 5 true
release conflicting-archive false false false "${identical[@]:0:7}" "${all[7]}=other"
expect conflicting-archive 1 0 -
release conflicting-legacy false false false "${all[0]}=legacy-other"; expect conflicting-legacy 1 0 -
release identical-legacy false false false "${all[0]}=legacy-same";   expect identical-legacy 0 7 true
release draft true false false;                                       expect draft 0 8 false
release prerelease false true false;                                  expect prerelease 0 8 false
release immutable-missing false false true;                           expect immutable-missing 1 0 -
release immutable-complete false false true "${identical[@]}";        expect immutable-complete 0 0 true
release unrelated-assets false false false "scrumboy-${tag}-windows-amd64.exe=other"
expect unrelated-assets 0 8 true

echo corrupted >> "$dist/scrumboy-cli_${tag}_linux_amd64.tar.gz"
release failed-checksum false false false;                            expect failed-checksum 1 0 -
rm -rf "$dist" && cp -r "$work/dist.pristine" "$dist"
rm "$dist/scrumboy-cli.rb"
release missing-formula false false false;                            expect missing-formula 1 0 -
rm -rf "$dist" && cp -r "$work/dist.pristine" "$dist"

if grep -hv '^[[:space:]]*#' "$workflow" "$script" | grep -q -- '--clobber'; then
  echo "FAIL workflow or publish script uses --clobber"; failures=$((failures + 1))
fi
if grep -q 'gh release upload' "$workflow"; then
  echo "FAIL the workflow uploads release assets outside publish-cli-release.sh"; failures=$((failures + 1))
fi
if ! grep -qE '^    needs: \[build, smoke\]' "$workflow"; then
  echo "FAIL the publish job does not wait for the smoke tests"; failures=$((failures + 1))
fi

[ "$failures" = 0 ] && echo "all publish-cli-release checks passed"
[ "$failures" = 0 ]
