#!/bin/sh
set -eu

PREFIX=/usr/local
PURGE=0
YES=0
HOME_DIR=/var/lib/agora
APPS_HOME=/var/lib/agora-apps

usage() {
	cat <<EOF
Uninstall Agora.

Usage: uninstall.sh [options]

  --prefix DIR   install prefix used by install.sh (default: /usr/local)
  --purge        also delete $HOME_DIR, $APPS_HOME and the agora user
  -y, --yes      do not ask for confirmation
  -h, --help     show this help
EOF
}

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--prefix) PREFIX=${2:?--prefix needs a value}; shift 2 ;;
	--prefix=*) PREFIX=${1#*=}; shift ;;
	--purge) PURGE=1; shift ;;
	-y | --yes) YES=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "unknown option: $1" ;;
	esac
done

[ "$(id -u)" -eq 0 ] || die "run as root (sudo)"

if [ "$PURGE" -eq 1 ]; then
	printf '\033[31mWARNING:\033[0m --purge permanently deletes %s (every workspace, browser profile, cookie, login and checkpoint), %s and the agora user.\n' "$HOME_DIR" "$APPS_HOME" >&2
	if [ "$YES" -eq 0 ]; then
		[ -r /dev/tty ] || die "no terminal to confirm: rerun with --yes to purge"
		printf 'Type "purge" to continue: ' >&2
		read -r answer </dev/tty || answer=
		[ "$answer" = purge ] || die "aborted"
	fi
fi

if [ -d /run/systemd/system ] && systemctl list-unit-files agora.service >/dev/null 2>&1; then
	say "Stopping agora.service"
	systemctl disable --now agora.service >/dev/null 2>&1 || true
fi
if [ -f /etc/systemd/system/agora.service ]; then
	rm -f /etc/systemd/system/agora.service
	[ ! -d /run/systemd/system ] || systemctl daemon-reload
fi

AGENT_USER=${SUDO_USER:-root}
id "$AGENT_USER" >/dev/null 2>&1 || AGENT_USER=root
as_user() {
	if [ "$AGENT_USER" = root ]; then
		sh -lc "$1"
	else
		runuser -u "$AGENT_USER" -- sh -lc "$1"
	fi
}
user_has() { as_user "command -v $1" >/dev/null 2>&1; }

if user_has claude && as_user "claude mcp get agora" >/dev/null 2>&1; then
	say "Removing the agora MCP server from Claude Code ($AGENT_USER)"
	as_user "claude mcp remove agora -s user" >/dev/null 2>&1 || warn "claude mcp remove failed"
fi
if user_has codex && as_user "codex mcp get agora" >/dev/null 2>&1; then
	say "Removing the agora MCP server from Codex ($AGENT_USER)"
	as_user "codex mcp remove agora" >/dev/null 2>&1 || warn "codex mcp remove failed"
fi
if user_has bunx; then
	runner="bunx skills"
elif user_has npx; then
	runner="npx -y skills"
else
	runner=
fi
if [ -n "$runner" ] && as_user "$runner ls -g" 2>/dev/null | grep -qw agora; then
	say "Removing the Agora skill"
	as_user "$runner remove agora -g -y" >/dev/null 2>&1 || warn "could not remove the skill ($runner remove agora -g -y)"
fi
if user_has paseo && as_user "paseo plugin ls --json" 2>/dev/null | grep -q "\"$PREFIX/share/agora/paseo-plugin\""; then
	say "Removing the Paseo plugin"
	as_user "paseo plugin remove agora" >/dev/null 2>&1 || warn "paseo plugin remove failed"
fi

say "Removing $PREFIX/bin/agora"
rm -f "$PREFIX/bin/agora"
rm -rf "$PREFIX/share/agora"

if [ "$PURGE" -eq 1 ]; then
	say "Deleting $HOME_DIR, $APPS_HOME and the agora user"
	pkill -KILL -u agora 2>/dev/null || true
	rm -rf "$HOME_DIR" "$APPS_HOME"
	if id agora >/dev/null 2>&1; then
		userdel agora 2>/dev/null || warn "could not delete the agora user"
	fi
else
	echo "  kept: $HOME_DIR (workspaces and the admin token) and the agora user; run with --purge to delete them"
fi
say "Agora uninstalled"
