#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION="${1:-0.1.0}"
ARCH="$(dpkg --print-architecture 2>/dev/null || echo amd64)"

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "error: dpkg-deb not found — this must run on Debian/Ubuntu" >&2
  exit 1
fi
if ! command -v go >/dev/null 2>&1; then
  echo "error: go not found — building the .deb still requires Go to compile the binaries" >&2
  exit 1
fi

pkg_root="$(mktemp -d)"
trap 'rm -rf "$pkg_root"' EXIT

mkdir -p "$pkg_root/DEBIAN" "$pkg_root/usr/bin" "$pkg_root/etc/nimbuscore" "$pkg_root/lib/systemd/system"

echo "==> building binaries (go, ${VERSION}, ${ARCH})"
(
  cd "$REPO_ROOT"
  go build -o "$pkg_root/usr/bin/nimbusctl" ./cmd/nimbusctl
  go build -o "$pkg_root/usr/bin/nimbus-apiserver" ./cmd/nimbus-apiserver
  go build -o "$pkg_root/usr/bin/nimbus-agent" ./cmd/nimbus-agent
)
chmod 0755 "$pkg_root"/usr/bin/*

install -m 0644 "$SCRIPT_DIR/nimbus-apiserver.service" "$pkg_root/lib/systemd/system/"
install -m 0644 "$SCRIPT_DIR/nimbus-agent.service" "$pkg_root/lib/systemd/system/"
install -m 0644 "$SCRIPT_DIR/apiserver.env" "$pkg_root/etc/nimbuscore/"
install -m 0644 "$SCRIPT_DIR/agent.env" "$pkg_root/etc/nimbuscore/"

install -m 0755 "$SCRIPT_DIR/postinst" "$pkg_root/DEBIAN/postinst"
install -m 0755 "$SCRIPT_DIR/postrm" "$pkg_root/DEBIAN/postrm"
install -m 0644 "$SCRIPT_DIR/conffiles" "$pkg_root/DEBIAN/conffiles"

installed_size="$(du -sk --exclude=DEBIAN "$pkg_root" | cut -f1)"

sed -e "s/@VERSION@/$VERSION/" -e "s/@ARCH@/$ARCH/" -e "s/@INSTALLED_SIZE@/$installed_size/" \
  "$SCRIPT_DIR/control.tmpl" > "$pkg_root/DEBIAN/control"

out="$REPO_ROOT/nimbuscore_${VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$pkg_root" "$out"

echo "==> built $out"
