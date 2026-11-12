#!/usr/bin/env bash
# Render the Homebrew formula for scrumboy-cli.
#
#   scripts/render-homebrew-formula.sh <version> <owner/repo> <checksums.txt> > Formula/scrumboy-cli.rb
#   scripts/render-homebrew-formula.sh --head-only <owner/repo>              > Formula/scrumboy-cli.rb
#
# Full mode installs the prebuilt release archives (macOS and Linux, amd64 and
# arm64) published by .github/workflows/cli-release.yml, and also supports
# `brew install --HEAD` (builds from source). --head-only emits just the source
# build, which is what the repo carries before its first CLI release.
#
# The repo is its own tap:  brew tap <owner>/scrumboy https://github.com/<owner>/scrumboy
set -euo pipefail

usage() {
  echo "usage: render-homebrew-formula.sh <version> <owner/repo> <checksums.txt> | --head-only <owner/repo>" >&2
  exit 2
}

head_only=false
if [ "${1:-}" = "--head-only" ]; then
  head_only=true
  repo="${2:-}"
  [ -n "$repo" ] || usage
else
  version="${1:-}"; repo="${2:-}"; sums="${3:-}"
  [ -n "$version" ] && [ -n "$repo" ] && [ -n "$sums" ] || usage
fi

sha() {
  local file="scrumboy-cli_${version}_$1_$2.tar.gz" s
  s="$(awk -v f="$file" '$2 == f {print $1}' "$sums")"
  if ! [[ "$s" =~ ^[0-9a-f]{64}$ ]]; then
    echo "no checksum for $file in $sums" >&2
    exit 1
  fi
  printf '%s' "$s"
}

url() { printf 'https://github.com/%s/releases/download/%s/scrumboy-cli_%s_%s_%s.tar.gz' "$repo" "$version" "$version" "$1" "$2"; }

cat <<EOF
class ScrumboyCli < Formula
  desc "Command-line client for the Scrumboy project board"
  homepage "https://github.com/${repo}"
  license "AGPL-3.0-only"
EOF

if ! $head_only; then
  cat <<EOF
  version "${version#v}"

  on_macos do
    on_arm do
      url "$(url darwin arm64)"
      sha256 "$(sha darwin arm64)"
    end
    on_intel do
      url "$(url darwin amd64)"
      sha256 "$(sha darwin amd64)"
    end
  end

  on_linux do
    on_arm do
      url "$(url linux arm64)"
      sha256 "$(sha linux arm64)"
    end
    on_intel do
      url "$(url linux amd64)"
      sha256 "$(sha linux amd64)"
    end
  end
EOF
fi

cat <<EOF

  head do
    url "https://github.com/${repo}.git", branch: "main"
    depends_on "go" => :build
  end

  def install
    if build.head?
      system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=HEAD"), "./cmd/scrumboy-cli"
    else
      bin.install "scrumboy-cli"
    end
  end

  test do
    assert_match "scrumboy-cli", shell_output("#{bin}/scrumboy-cli version")
  end
end
EOF
