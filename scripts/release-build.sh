#!/bin/sh
set -eu

TAG=${1:?usage: release-build.sh vX.Y.Z}
ARCH=$(dpkg --print-architecture)
NAME="agora-$TAG-linux-$ARCH"

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends libx264-dev libva-dev pkg-config >/dev/null
rm -f "/usr/lib/$(gcc -dumpmachine)/libx264.so"
printf '#!/bin/sh\nexec pkg-config --static "$@"\n' >/usr/local/bin/pkg-config-static
chmod +x /usr/local/bin/pkg-config-static

make build PKG_CONFIG=pkg-config-static VERSION="${TAG#v}" COMMIT="${COMMIT:-unknown}" DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if ldd bin/agora | grep -q x264; then
	echo "bin/agora links libx264 dynamically" >&2
	exit 1
fi
bin/agora version

rm -rf "dist/$NAME"
mkdir -p "dist/$NAME"
cp bin/agora deploy/agora.service LICENSE-MIT LICENSE-APACHE README.md CHANGELOG.md "dist/$NAME/"
tar -C . --exclude node_modules -cf - paseo-plugin | tar -C "dist/$NAME" -xf -
tar -C dist -czf "dist/$NAME.tar.gz" "$NAME"
rm -rf "dist/$NAME"
echo "dist/$NAME.tar.gz"
