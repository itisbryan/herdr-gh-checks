#!/bin/sh
# herdr [[build]] step. Download the prebuilt herdr-gh-checks binary matching this manifest's
# version for the host OS/arch (verify SHA-256) into ./herdr-gh-checks. On ANY miss (no release,
# unsupported platform, no curl, checksum fail) fall back to `go build` — so a Go toolchain is
# only needed when there is no matching prebuilt binary.
set -eu

bin=herdr-gh-checks
repo=itisbryan/herdr-gh-checks
version=$(sed -n 's/^version = "\(.*\)"/\1/p' herdr-plugin.toml | head -1)

os=$(uname -s); arch=$(uname -m)
case "$os" in Darwin) os=darwin ;; Linux) os=linux ;; *) os="" ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) arch="" ;; esac

sha256() { shasum -a 256 "$1" 2>/dev/null | awk '{print $1}' || sha256sum "$1" | awk '{print $1}'; }

build_from_source() {
  if command -v go >/dev/null 2>&1; then
    echo "herdr-gh-checks: building from source with go"
    go build -trimpath -ldflags "-s -w" -o "$bin" .
    exit 0
  fi
  echo "herdr-gh-checks: no prebuilt binary for ${os:-?}/${arch:-?} and Go is not installed." >&2
  echo "  Install Go 1.25+ (to build from source) or use a supported platform (linux/macOS, amd64/arm64)." >&2
  exit 1
}

[ -n "$os" ] && [ -n "$arch" ] && [ -n "$version" ] && command -v curl >/dev/null 2>&1 || build_from_source

asset="${bin}-${os}-${arch}"
base="https://github.com/${repo}/releases/download/v${version}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if curl -fsSL "$base/$asset" -o "$tmp/$bin" && curl -fsSL "$base/$asset.sha256" -o "$tmp/sum"; then
  want=$(cat "$tmp/sum"); got=$(sha256 "$tmp/$bin")
  if [ "$want" = "$got" ]; then
    chmod +x "$tmp/$bin"; mv "$tmp/$bin" "./$bin"
    echo "herdr-gh-checks: installed prebuilt $asset v$version"
    exit 0
  fi
  echo "herdr-gh-checks: checksum mismatch for $asset; falling back to source" >&2
fi
build_from_source
