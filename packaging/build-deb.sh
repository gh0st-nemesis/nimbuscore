#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION="${1:-0.1.0}"
ARCH="${DEB_ARCH:-amd64}"
GOARCH_TARGET="${GOARCH_TARGET:-amd64}"
BUILDKIT_VERSION="${BUILDKIT_VERSION:-v0.31.2}"

for tool in ar tar gzip go curl; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: required tool '$tool' not found on PATH" >&2
    exit 1
  fi
done

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

data_dir="$work_dir/data"
control_dir="$work_dir/control"
mkdir -p "$data_dir/usr/bin" "$data_dir/usr/local/bin" "$data_dir/etc/nimbuscore" "$data_dir/etc/buildkit" \
  "$data_dir/lib/systemd/system" "$control_dir"

echo "==> cross-compiling linux/${GOARCH_TARGET} binaries (go, version ${VERSION})"
(
  cd "$REPO_ROOT"
  GOOS=linux GOARCH="$GOARCH_TARGET" CGO_ENABLED=0 go build -o "$data_dir/usr/bin/nimbusctl" ./cmd/nimbusctl
  GOOS=linux GOARCH="$GOARCH_TARGET" CGO_ENABLED=0 go build -o "$data_dir/usr/bin/nimbus-apiserver" ./cmd/nimbus-apiserver
  GOOS=linux GOARCH="$GOARCH_TARGET" CGO_ENABLED=0 go build -o "$data_dir/usr/bin/nimbus-agent" ./cmd/nimbus-agent
)

echo "==> fetching buildkit ${BUILDKIT_VERSION} (buildctl, buildkitd)"
buildkit_tarball="$work_dir/buildkit.tar.gz"
curl -sL -o "$buildkit_tarball" \
  "https://github.com/moby/buildkit/releases/download/${BUILDKIT_VERSION}/buildkit-${BUILDKIT_VERSION}.linux-${GOARCH_TARGET}.tar.gz"
tar -xzf "$buildkit_tarball" -C "$work_dir" bin/buildctl bin/buildkitd
cp "$work_dir/bin/buildctl" "$data_dir/usr/local/bin/buildctl"
cp "$work_dir/bin/buildkitd" "$data_dir/usr/local/bin/buildkitd"
chmod 0755 "$data_dir/usr/local/bin/buildctl" "$data_dir/usr/local/bin/buildkitd"

cp "$SCRIPT_DIR/nimbus-apiserver.service" "$data_dir/lib/systemd/system/"
cp "$SCRIPT_DIR/nimbus-agent.service" "$data_dir/lib/systemd/system/"
cp "$SCRIPT_DIR/buildkitd.service" "$data_dir/lib/systemd/system/"
cp "$SCRIPT_DIR/apiserver.env" "$data_dir/etc/nimbuscore/"
cp "$SCRIPT_DIR/agent.env" "$data_dir/etc/nimbuscore/"
cp "$SCRIPT_DIR/buildkitd.toml" "$data_dir/etc/buildkit/"

installed_size="$(du -sk "$data_dir" | cut -f1)"

sed -e "s/@VERSION@/$VERSION/" -e "s/@ARCH@/$ARCH/" -e "s/@INSTALLED_SIZE@/$installed_size/" \
  "$SCRIPT_DIR/control.tmpl" > "$control_dir/control"
cp "$SCRIPT_DIR/postinst" "$control_dir/postinst"
cp "$SCRIPT_DIR/postrm" "$control_dir/postrm"
cp "$SCRIPT_DIR/config" "$control_dir/config"
cp "$SCRIPT_DIR/conffiles" "$control_dir/conffiles"
cp "$SCRIPT_DIR/templates" "$control_dir/templates"

echo "2.0" > "$work_dir/debian-binary"

control_tar="$work_dir/control.tar"
tar --owner=0 --group=0 --numeric-owner --mode=0755 --no-recursion -cf "$control_tar" -C "$control_dir" \
  ./postinst ./postrm ./config
tar --owner=0 --group=0 --numeric-owner --mode=0644 --no-recursion -rf "$control_tar" -C "$control_dir" \
  ./control ./conffiles ./templates
gzip -n -f "$control_tar"

data_tar="$work_dir/data.tar"
tar --owner=0 --group=0 --numeric-owner --mode=0755 --no-recursion -cf "$data_tar" -C "$data_dir" \
  ./usr ./usr/bin ./usr/bin/nimbusctl ./usr/bin/nimbus-apiserver ./usr/bin/nimbus-agent \
  ./usr/local ./usr/local/bin ./usr/local/bin/buildctl ./usr/local/bin/buildkitd \
  ./etc ./etc/nimbuscore ./etc/buildkit ./lib ./lib/systemd ./lib/systemd/system
tar --owner=0 --group=0 --numeric-owner --mode=0644 --no-recursion -rf "$data_tar" -C "$data_dir" \
  ./etc/nimbuscore/apiserver.env ./etc/nimbuscore/agent.env ./etc/buildkit/buildkitd.toml \
  ./lib/systemd/system/nimbus-apiserver.service ./lib/systemd/system/nimbus-agent.service \
  ./lib/systemd/system/buildkitd.service
gzip -n -f "$data_tar"

out="$REPO_ROOT/nimbuscore_${VERSION}_${ARCH}.deb"
rm -f "$out"
( cd "$work_dir" && ar rc "$out" debian-binary control.tar.gz data.tar.gz )

echo "==> built $out"
