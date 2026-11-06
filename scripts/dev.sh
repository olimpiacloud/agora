#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
PIDF=/tmp/carbon-logs/daemon.pid
mkdir -p /tmp/carbon-logs
stop() {
  if [[ -f $PIDF ]] && kill -0 "$(cat $PIDF)" 2>/dev/null; then
    kill -INT "$(cat $PIDF)"
    for _ in $(seq 1 60); do kill -0 "$(cat $PIDF)" 2>/dev/null || break; sleep 0.5; done
  fi
  rm -f $PIDF
}
case "${1:-restart}" in
  stop) stop ;;
  build) go build -o bin/carbon ./cmd/carbon ;;
  restart)
    go build -o bin/carbon ./cmd/carbon
    stop
    nohup ./bin/carbon serve --home "${CARBON_HOME:-/var/lib/carbon}" >>/tmp/carbon-logs/daemon.log 2>&1 &
    echo $! > $PIDF
    for _ in $(seq 1 40); do curl -sf http://127.0.0.1:7788/v1/health >/dev/null && break; sleep 0.1; done
    echo "carbon pid $(cat $PIDF)"
    ;;
esac
