#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$INSTALL_DIR/vaps"
CONFIG="$INSTALL_DIR/config.toml"
PIDFILE="$INSTALL_DIR/vaps.pid"
LOGFILE="$INSTALL_DIR/logs/startup.log"

usage() {
  cat <<'USAGE'
Usage: start.sh {start|stop|status|restart}

  start    Start vaps in the background
  stop     Stop the background vaps process
  status   Show whether vaps is running
  restart  Stop then start vaps
USAGE
}

is_running() {
  [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null
}

start_server() {
  if is_running; then
    echo "vaps already running (pid $(cat "$PIDFILE"))"
    return 0
  fi

  mkdir -p "$INSTALL_DIR/logs" "$INSTALL_DIR/data"
  cd "$INSTALL_DIR"

  nohup "$BIN" --config "$CONFIG" >>"$LOGFILE" 2>&1 &
  echo $! >"$PIDFILE"
  sleep 0.2

  if is_running; then
    echo "vaps started (pid $(cat "$PIDFILE"))"
    echo "config: $CONFIG"
    echo "logs:   $INSTALL_DIR/logs/"
  else
    echo "vaps failed to start; see $LOGFILE" >&2
    rm -f "$PIDFILE"
    return 1
  fi
}

stop_server() {
  if ! is_running; then
    echo "vaps is not running"
    rm -f "$PIDFILE"
    return 0
  fi

  local pid
  pid=$(cat "$PIDFILE")
  kill "$pid"
  for _ in $(seq 1 20); do
    if kill -0 "$pid" 2>/dev/null; then
      sleep 0.5
    else
      rm -f "$PIDFILE"
      echo "vaps stopped"
      return 0
    fi
  done

  echo "vaps did not stop gracefully; sending SIGKILL" >&2
  kill -9 "$pid" 2>/dev/null || true
  rm -f "$PIDFILE"
}

cmd=${1:-start}
case "$cmd" in
  start) start_server ;;
  stop) stop_server ;;
  status)
    if is_running; then
      echo "vaps running (pid $(cat "$PIDFILE"))"
    else
      echo "vaps not running"
      exit 1
    fi
    ;;
  restart)
    stop_server || true
    start_server
    ;;
  -h|--help|help) usage ;;
  *) usage; exit 1 ;;
esac
