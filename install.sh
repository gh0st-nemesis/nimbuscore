#!/usr/bin/env bash
set -euo pipefail

PREFIX="${PREFIX:-/usr/local/bin}"
MIN_GO_MAJOR=1
MIN_GO_MINOR=25

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

if ! command -v go >/dev/null 2>&1; then
  echo "error: go is not installed or not on PATH" >&2
  echo "install Go ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+ first, e.g.:" >&2
  echo "  curl -LO https://go.dev/dl/go1.25.0.linux-amd64.tar.gz" >&2
  echo "  sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz" >&2
  echo "  export PATH=\$PATH:/usr/local/go/bin" >&2
  exit 1
fi

go_version="$(go env GOVERSION)"
go_version="${go_version#go}"
go_major="${go_version%%.*}"
go_rest="${go_version#*.}"
go_minor="${go_rest%%.*}"

if (( go_major < MIN_GO_MAJOR || (go_major == MIN_GO_MAJOR && go_minor < MIN_GO_MINOR) )); then
  echo "error: found go ${go_version}, NimbusCore requires go ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+" >&2
  exit 1
fi

binaries=(nimbusctl nimbus-apiserver nimbus-agent)

echo "==> building ${binaries[*]} (go ${go_version})"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

for bin in "${binaries[@]}"; do
  go build -o "$tmp_dir/$bin" "./cmd/$bin"
done

echo "==> installing to ${PREFIX}"
mkdir -p "$PREFIX" 2>/dev/null || true
for bin in "${binaries[@]}"; do
  if [[ -w "$PREFIX" ]]; then
    install -m 0755 "$tmp_dir/$bin" "$PREFIX/$bin"
  else
    sudo install -m 0755 "$tmp_dir/$bin" "$PREFIX/$bin"
  fi
  echo "  $PREFIX/$bin"
done

echo
echo "$("$PREFIX/nimbusctl" version 2>&1 | sed 's/^/nimbusctl version: /')"
echo "done — see README.md for how to bootstrap a cluster (nimbus-apiserver -bootstrap ...)."
