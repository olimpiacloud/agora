#!/bin/sh
set -eu

REPO=olimpiacloud/agora
VERSION=
PREFIX=/usr/local
AGENTS=1
PASEO=1
SOURCE=
HOME_DIR=/var/lib/agora
APPS_HOME=/var/lib/agora-apps
GO_VERSION=1.27.4

usage() {
	cat <<EOF
Install Agora on Ubuntu or Debian (amd64, arm64).

Usage: install.sh [options]

  --version vX.Y.Z   release to install (default: latest)
  --prefix DIR       install prefix (default: /usr/local)
  --source DIR       build from this source checkout instead of downloading
  --no-agents        do not configure Claude Code, Codex, the skill or Paseo
  --no-paseo         do not install the Paseo plugin
  -h, --help         show this help
EOF
}

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--version) VERSION=${2:?--version needs a value}; shift 2 ;;
	--version=*) VERSION=${1#*=}; shift ;;
	--prefix) PREFIX=${2:?--prefix needs a value}; shift 2 ;;
	--prefix=*) PREFIX=${1#*=}; shift ;;
	--source) SOURCE=${2:?--source needs a value}; shift 2 ;;
	--source=*) SOURCE=${1#*=}; shift ;;
	--no-agents) AGENTS=0; shift ;;
	--no-paseo) PASEO=0; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "unknown option: $1" ;;
	esac
done

case "$VERSION" in
"" | v*) ;;
*) VERSION=v$VERSION ;;
esac

[ "$(id -u)" -eq 0 ] || die "run as root, for example: curl -fsSL https://agora.olimpia.dev/install.sh | sudo sh"
[ -r /etc/os-release ] || die "cannot detect the distribution (/etc/os-release missing)"
OS_ID=$(. /etc/os-release && echo "${ID:-unknown}")
case " $OS_ID $(. /etc/os-release && echo "${ID_LIKE:-}") " in
*" debian "* | *" ubuntu "*) ;;
*) die "unsupported distribution: $OS_ID (Ubuntu or Debian required)" ;;
esac
ARCH=$(dpkg --print-architecture)
case "$ARCH" in
amd64 | arm64) ;;
*) die "unsupported architecture: $ARCH (amd64 or arm64 required)" ;;
esac

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM
export DEBIAN_FRONTEND=noninteractive
APT_UPDATED=0
CHANGED=0

apt_install() {
	if [ "$APT_UPDATED" -eq 0 ]; then
		apt-get update -qq
		APT_UPDATED=1
	fi
	apt-get install -y -qq --no-install-recommends "$@" >/dev/null
}

have() { command -v "$1" >/dev/null 2>&1; }

install_deps() {
	say "Installing dependencies"
	missing=
	for p in xvfb libva2 libva-drm2 ca-certificates curl; do
		dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q "ok installed" || missing="$missing $p"
	done
	[ -z "$missing" ] || apt_install $missing
	if have google-chrome || have google-chrome-stable || have chromium || have chromium-browser; then
		return
	fi
	if [ "$ARCH" = amd64 ]; then
		say "Installing Google Chrome"
		curl -fsSL -o "$TMP/chrome.deb" https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
		apt_install "$TMP/chrome.deb"
	else
		say "Installing Chromium"
		apt_install chromium || apt_install chromium-browser || die "could not install Chromium"
		if [ "$OS_ID" = ubuntu ]; then
			warn "on Ubuntu, Chromium is a snap; if Agora cannot start it, install Chromium from a deb source"
		fi
	fi
}

create_user() {
	if ! id agora >/dev/null 2>&1; then
		say "Creating the unprivileged agora user"
		useradd --system --create-home --home-dir "$APPS_HOME" --shell /usr/sbin/nologin agora
	fi
	if ! id agora-x >/dev/null 2>&1; then
		say "Creating the agora-x user for the X servers"
		useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin agora-x
		CHANGED=1
	fi
}

latest_version() {
	curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null |
		sed -n 's/.*"tag_name": *"\(v[^"]*\)".*/\1/p' | head -n 1
}

download_release() {
	[ -n "$VERSION" ] || VERSION=$(latest_version)
	[ -n "$VERSION" ] || die "could not look up the latest Agora release on GitHub (network or API rate limit?): retry later, pass --version vX.Y.Z, or build a local checkout with --source DIR"
	name="agora-$VERSION-linux-$ARCH"
	base=${AGORA_DOWNLOAD_BASE:-https://github.com/$REPO/releases/download/$VERSION}
	say "Downloading Agora $VERSION ($ARCH)"
	curl -fsSL -o "$TMP/$name.tar.gz" "$base/$name.tar.gz" || die "could not download $name.tar.gz from $base: check --version, or build a local checkout with --source DIR"
	curl -fsSL -o "$TMP/SHA256SUMS" "$base/SHA256SUMS" || die "SHA256SUMS missing from release $VERSION"
	(cd "$TMP" && grep " $name.tar.gz\$" SHA256SUMS | sha256sum -c --status) || die "checksum mismatch for $name.tar.gz"
	tar -C "$TMP" -xzf "$TMP/$name.tar.gz"
	PKG="$TMP/$name"
	BIN="$PKG/agora"
	UNIT="$PKG/agora.service"
	PLUGIN="$PKG/paseo-plugin"
}

build_from_source() {
	say "Building Agora from source"
	apt_install build-essential pkg-config libx264-dev libva-dev git
	[ -f "$SOURCE/go.mod" ] || die "$SOURCE is not an Agora checkout"
	gobin=$(command -v go || true)
	if [ -z "$gobin" ] || ! "$gobin" version | grep -q "go$GO_VERSION"; then
		say "Downloading Go $GO_VERSION"
		tgz="go$GO_VERSION.linux-$ARCH.tar.gz"
		curl -fsSL -o "$TMP/$tgz" "https://dl.google.com/go/$tgz"
		sum=$(curl -fsSL "https://dl.google.com/go/$tgz.sha256")
		echo "$sum  $TMP/$tgz" | sha256sum -c --status || die "checksum mismatch for $tgz"
		tar -C "$TMP" -xzf "$TMP/$tgz"
		gobin="$TMP/go/bin/go"
	fi
	ver=${VERSION#v}
	[ -n "$ver" ] || ver=$(git -C "$SOURCE" describe --tags --match 'v[0-9]*' 2>/dev/null | sed 's/^v//' || true)
	[ -n "$ver" ] || ver=dev
	commit=$(git -C "$SOURCE" rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
	pkg=github.com/olimpiacloud/agora/internal/core
	(cd "$SOURCE" && GOPATH="$TMP/gopath" GOCACHE="$TMP/gocache" GOFLAGS=-mod=readonly GOTOOLCHAIN=local \
		"$gobin" build -trimpath -ldflags "-s -w -X $pkg.Version=$ver -X $pkg.Commit=$commit -X $pkg.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o "$TMP/agora-bin" ./cmd/agora) || die "build failed"
	BIN="$TMP/agora-bin"
	UNIT="$SOURCE/deploy/agora.service"
	PLUGIN="$SOURCE/paseo-plugin"
}

install_files() {
	say "Installing $PREFIX/bin/agora"
	install -d "$PREFIX/bin" "$PREFIX/share/agora"
	if ! cmp -s "$BIN" "$PREFIX/bin/agora"; then
		install -m 0755 "$BIN" "$PREFIX/bin/agora.new"
		mv -f "$PREFIX/bin/agora.new" "$PREFIX/bin/agora"
		CHANGED=1
	fi
	if [ -d "$PLUGIN" ]; then
		rm -rf "$PREFIX/share/agora/paseo-plugin"
		cp -R "$PLUGIN" "$PREFIX/share/agora/paseo-plugin"
		rm -rf "$PREFIX/share/agora/paseo-plugin/node_modules"
	fi
	sed "s#/usr/local/bin/agora#$PREFIX/bin/agora#g" "$UNIT" >"$TMP/agora.service"
	if ! cmp -s "$TMP/agora.service" /etc/systemd/system/agora.service 2>/dev/null; then
		install -m 0644 "$TMP/agora.service" /etc/systemd/system/agora.service
		CHANGED=1
	fi
}

start_service() {
	if [ ! -d /run/systemd/system ]; then
		warn "systemd is not running: start Agora with '$PREFIX/bin/agora serve'"
		return
	fi
	systemctl daemon-reload
	systemctl enable agora.service >/dev/null 2>&1
	if [ "$CHANGED" -eq 1 ] || ! systemctl is-active --quiet agora.service; then
		say "Starting agora.service"
		systemctl restart agora.service
	fi
	i=0
	while [ ! -s "$HOME_DIR/admin.token" ] && [ $i -lt 50 ]; do
		sleep 0.2
		i=$((i + 1))
	done
	systemctl is-active --quiet agora.service || die "agora.service failed to start: journalctl -u agora"
}

as_user() {
	if [ "$AGENT_USER" = root ]; then
		sh -lc "$1"
	else
		runuser -u "$AGENT_USER" -- sh -lc "$1"
	fi
}

user_has() { as_user "command -v $1" >/dev/null 2>&1; }

save_token() {
	user_home=$(getent passwd "$AGENT_USER" | cut -d: -f6)
	case "$user_home" in
	/*) ;;
	*) warn "no home directory for $AGENT_USER: the MCP server will need AGORA_TOKEN or AGORA_TOKEN_FILE"; return ;;
	esac
	dir="$user_home/.config/agora"
	say "Saving the admin token for $AGENT_USER in $dir/token"
	if runuser -u "$AGENT_USER" -- sh -c 'umask 077 && mkdir -p "$1" && chmod 700 "$1" && cat >"$1/token.new" && chmod 600 "$1/token.new" && mv -f "$1/token.new" "$1/token"' sh "$dir" <"$HOME_DIR/admin.token"; then
		envflag="AGORA_TOKEN_FILE=$dir/token"
	else
		warn "could not write $dir/token"
	fi
}

inline_token() {
	printf '%s\n' "$1" | grep -Eq 'AGORA_TOKEN([^_]|$)'
}

migrate_mcp() {
	if [ -z "$envflag" ] && [ "$AGENT_USER" != root ]; then
		warn "the agora MCP server in $1 ($AGENT_USER) has an inline AGORA_TOKEN and no token file could be saved: leaving it as is"
		return 1
	fi
	say "Moving the inline AGORA_TOKEN of the agora MCP server in $1 ($AGENT_USER) to ${envflag:-the admin token file}"
	if ! as_user "$2" >/dev/null 2>&1; then
		warn "could not remove the agora MCP server from $1 ($2)"
		return 1
	fi
	MIGRATED=1
}

configure_agents() {
	AGENT_USER=${SUDO_USER:-root}
	id "$AGENT_USER" >/dev/null 2>&1 || AGENT_USER=root
	envflag=
	MIGRATED=0
	if [ "$AGENT_USER" != root ] && [ -s "$HOME_DIR/admin.token" ] && { user_has claude || user_has codex; }; then
		save_token
	fi
	configured=0
	if user_has claude; then
		add=1
		if cfg=$(as_user "claude mcp get agora" 2>/dev/null); then
			add=0
			if inline_token "$cfg"; then
				if migrate_mcp "Claude Code" "claude mcp remove agora -s user"; then
					add=1
				fi
			else
				say "Claude Code already has the agora MCP server"
			fi
		fi
		if [ "$add" -eq 1 ]; then
			say "Adding the agora MCP server to Claude Code ($AGENT_USER)"
			as_user "claude mcp add agora -s user ${envflag:+-e '$envflag'} -- $PREFIX/bin/agora mcp" >/dev/null || warn "claude mcp add failed"
		fi
		configured=1
	fi
	if user_has codex; then
		add=1
		if cfg=$(as_user "codex mcp get agora" 2>/dev/null); then
			add=0
			if inline_token "$cfg"; then
				if migrate_mcp "Codex" "codex mcp remove agora"; then
					add=1
				fi
			else
				say "Codex already has the agora MCP server"
			fi
		fi
		if [ "$add" -eq 1 ]; then
			say "Adding the agora MCP server to Codex ($AGENT_USER)"
			as_user "codex mcp add agora ${envflag:+--env '$envflag'} -- $PREFIX/bin/agora mcp" >/dev/null || warn "codex mcp add failed"
		fi
		configured=1
	fi
	if [ "$MIGRATED" -eq 1 ]; then
		warn "the admin token was stored in plain text in the agent configuration: rotate it with 'sudo rm $HOME_DIR/admin.token && sudo systemctl restart agora', then run this installer again to save the new token"
	fi
	if [ "$configured" -eq 1 ]; then
		if user_has bunx; then
			runner="bunx skills"
		elif user_has npx; then
			runner="npx -y skills"
		else
			runner=
		fi
		if [ -n "$runner" ]; then
			say "Installing the Agora skill"
			as_user "$runner add $REPO-skills -g -y" >/dev/null 2>&1 || warn "could not install the skill ($runner add $REPO-skills -g -y)"
		fi
	fi
	if [ "$PASEO" -eq 1 ] && user_has paseo && [ -d "$PREFIX/share/agora/paseo-plugin" ]; then
		if as_user "paseo plugin ls -q" 2>/dev/null | grep -qx agora; then
			say "Paseo already has an agora plugin"
		else
			say "Installing the Paseo plugin"
			as_user "paseo plugin install $PREFIX/share/agora/paseo-plugin" >/dev/null || warn "paseo plugin install failed"
		fi
	fi
}

install_deps
create_user
if [ -n "$SOURCE" ]; then
	build_from_source
else
	download_release
fi
install_files
start_service
[ "$AGENTS" -eq 0 ] || configure_agents

say "Agora $("$PREFIX/bin/agora" version | sed 's/^agora //') installed"
echo "  dashboard:   http://127.0.0.1:7788 (paste the token from 'sudo agora token')"
echo "  admin token: $HOME_DIR/admin.token"
echo "  uninstall:   curl -fsSL https://agora.olimpia.dev/uninstall.sh | sudo sh"
