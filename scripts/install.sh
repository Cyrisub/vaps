#!/usr/bin/env bash
# Install vaps from GitHub releases on Linux.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Cyrisub/vaps/dev/scripts/install.sh | bash
#   ./scripts/install.sh --install-dir /opt/vaps --channel release
#   ./scripts/install.sh --install-dir ~/vaps --channel pre-release
#
# Generated beside the binary:
#   config.toml         default config (created on first install only)
#   vaps-install.conf   install metadata (repo, channel, arch, version)
#   start.sh            start/stop/restart/status in background
#   update.sh           download and replace the binary

set -euo pipefail

GITHUB_REPO="${GITHUB_REPO:-Cyrisub/vaps}"
INSTALL_DIR="${INSTALL_DIR:-/opt/vaps}"
CHANNEL="${CHANNEL:-release}"
PINNED_VERSION=""
START_AFTER_INSTALL=1

usage() {
  cat <<'EOF'
Usage: install.sh [options]

Install vaps from GitHub releases and generate helper scripts in the install
directory.

Options:
  --install-dir DIR       Install directory (default: /opt/vaps)
  --channel CHANNEL       release or pre-release (default: release)
  --version VERSION       Install a specific tag, e.g. v1.0.0 or 1.0.0
  --repo OWNER/REPO       GitHub repository (default: Cyrisub/vaps)
  --no-start              Do not start vaps after install
  -h, --help              Show this help

Channels:
  release       Latest formal GitHub release (non-prerelease)
  pre-release   Latest GitHub pre-release

Examples:
  ./scripts/install.sh
  ./scripts/install.sh --install-dir /usr/local/vaps --channel pre-release
  curl -fsSL .../install.sh | bash -s -- --install-dir "$HOME/vaps"
EOF
}

log() {
  printf '[install] %s\n' "$*"
}

die() {
  printf '[install] error: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  local cmd=$1
  command -v "$cmd" >/dev/null 2>&1 || die "missing required command: $cmd"
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --install-dir)
        [ $# -ge 2 ] || die "--install-dir requires a value"
        INSTALL_DIR=$2
        shift 2
        ;;
      --channel)
        [ $# -ge 2 ] || die "--channel requires a value"
        CHANNEL=$2
        shift 2
        ;;
      --version)
        [ $# -ge 2 ] || die "--version requires a value"
        PINNED_VERSION=$2
        shift 2
        ;;
      --repo)
        [ $# -ge 2 ] || die "--repo requires a value"
        GITHUB_REPO=$2
        shift 2
        ;;
      --no-start)
        START_AFTER_INSTALL=0
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown option: $1 (use --help)"
        ;;
    esac
  done

  case "$CHANNEL" in
    release|pre-release) ;;
    *) die "invalid channel: $CHANNEL (expected release or pre-release)" ;;
  esac

  INSTALL_DIR=$(python3 -c 'import os,sys; print(os.path.expanduser(sys.argv[1]))' "$INSTALL_DIR")
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *)
      die "unsupported architecture: $(uname -m) (supported: amd64, arm64)"
      ;;
  esac
}

resolve_release_json() {
  local repo=$1
  local channel=$2
  local pinned=$3

  python3 - "$repo" "$channel" "$pinned" <<'PY'
import json
import sys
import urllib.error
import urllib.request

repo, channel, pinned = sys.argv[1:4]
headers = {
    "Accept": "application/vnd.github+json",
    "User-Agent": "vaps-installer",
}

def fetch(url):
    req = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(req) as resp:
        return json.load(resp)

if pinned:
    tag = pinned if pinned.startswith("v") else f"v{pinned}"
    try:
        release = fetch(f"https://api.github.com/repos/{repo}/releases/tags/{tag}")
    except urllib.error.HTTPError as exc:
        raise SystemExit(f"release tag not found: {tag} ({exc.code})")
    print(json.dumps(release))
    raise SystemExit(0)

if channel == "release":
    release = fetch(f"https://api.github.com/repos/{repo}/releases/latest")
    if release.get("draft"):
        raise SystemExit("latest release is a draft; nothing to install")
    if release.get("prerelease"):
        raise SystemExit("latest release is a pre-release; use --channel pre-release")
    print(json.dumps(release))
    raise SystemExit(0)

releases = fetch(f"https://api.github.com/repos/{repo}/releases?per_page=100")
for release in releases:
    if release.get("prerelease") and not release.get("draft"):
        print(json.dumps(release))
        raise SystemExit(0)

raise SystemExit("no pre-release found")
PY
}

pick_linux_asset() {
  local release_json=$1
  local arch=$2

  python3 - "$arch" <<'PY' <<<"$release_json"
import json
import sys

arch = sys.argv[1]
release = json.load(sys.stdin)
tag = release["tag_name"]
version = tag[1:] if tag.startswith("v") else tag
expected = f"vaps-{version}-linux-{arch}.tar.gz"

for asset in release.get("assets", []):
    if asset.get("name") == expected:
        print(json.dumps({
            "tag": tag,
            "version": version,
            "asset_name": expected,
            "download_url": asset["browser_download_url"],
        }))
        raise SystemExit(0)

names = ", ".join(asset.get("name", "?") for asset in release.get("assets", []))
raise SystemExit(f"asset not found: {expected}; available: {names or '(none)'}")
PY
}

download_binary() {
  local install_dir=$1
  local download_url=$2
  local tmpdir

  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' RETURN

  log "downloading $(basename "$download_url")"
  curl -fsSL -L -o "$tmpdir/archive.tar.gz" "$download_url"
  tar -xzf "$tmpdir/archive.tar.gz" -C "$tmpdir"

  local extracted
  extracted=$(find "$tmpdir" -maxdepth 1 -type f -name 'vaps' | head -n 1)
  [ -n "$extracted" ] || die "archive did not contain a vaps binary"

  install -m 0755 "$extracted" "$install_dir/vaps"
}

write_default_config() {
  local install_dir=$1
  local config_path="$install_dir/config.toml"

  if [ -f "$config_path" ]; then
    log "keeping existing config: $config_path"
    return 0
  fi

  cat >"$config_path" <<'EOF'
# vaps default configuration (TOML v1.1.0)
#:tombi toml-version = "v1.1.0"

addr = ":8588"
data_dir = "data"
metadata_db = "data/metadata.db"
auth_db = "data/auth.db"
upload_db = "data/uploads.db"
log_dir = "logs"
log_retention_days = 7
status_log_interval = "1m"

[cache]
bytes = "64MiB"
max_object_bytes = "4MiB"

[auth]
expire_time = "30m"

[upload]
direct_max_bytes = "8MiB"
expiration = "24h"
cleanup_interval = "1m"
dir = "data/uploads"

[backup]
backend = []
flush_interval = "1m"
max_pending = 50
svn = {
    url = "",
    bin = "svn",
    mucc_bin = "svnmucc",
}
EOF
  chmod 0644 "$config_path"
  log "created default config: $config_path"
}

write_install_conf() {
  local install_dir=$1
  local repo=$2
  local channel=$3
  local arch=$4
  local tag=$5

  cat >"$install_dir/vaps-install.conf" <<EOF
# Generated by scripts/install.sh. Used by update.sh.
GITHUB_REPO="$repo"
CHANNEL="$channel"
ARCH="$arch"
INSTALL_DIR="$install_dir"
INSTALLED_TAG="$tag"
EOF
  chmod 0644 "$install_dir/vaps-install.conf"
}

write_start_script() {
  local install_dir=$1

  cat >"$install_dir/start.sh" <<'EOF'
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

  nohup "$BIN" -config "$CONFIG" >>"$LOGFILE" 2>&1 &
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
EOF
  chmod 0755 "$install_dir/start.sh"
}

write_update_script() {
  local install_dir=$1

  cat >"$install_dir/update.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_DIR="$INSTALL_DIR"
CONF="$INSTALL_DIR/vaps-install.conf"
START_SCRIPT="$INSTALL_DIR/start.sh"

usage() {
  cat <<'USAGE'
Usage: update.sh [options]

Download the latest release for the configured channel and replace the vaps
binary. Existing config.toml and data are preserved.

Options:
  --channel CHANNEL   release or pre-release (default: value from vaps-install.conf)
  --version VERSION   Install a specific tag, e.g. v1.0.0
  --restart           Restart vaps after a successful update
  -h, --help          Show this help
USAGE
}

log() {
  printf '[update] %s\n' "$*"
}

die() {
  printf '[update] error: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

resolve_release_json() {
  local repo=$1
  local channel=$2
  local pinned=$3

  python3 - "$repo" "$channel" "$pinned" <<'PY'
import json
import sys
import urllib.error
import urllib.request

repo, channel, pinned = sys.argv[1:4]
headers = {
    "Accept": "application/vnd.github+json",
    "User-Agent": "vaps-installer",
}

def fetch(url):
    req = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(req) as resp:
        return json.load(resp)

if pinned:
    tag = pinned if pinned.startswith("v") else f"v{pinned}"
    try:
        release = fetch(f"https://api.github.com/repos/{repo}/releases/tags/{tag}")
    except urllib.error.HTTPError as exc:
        raise SystemExit(f"release tag not found: {tag} ({exc.code})")
    print(json.dumps(release))
    raise SystemExit(0)

if channel == "release":
    release = fetch(f"https://api.github.com/repos/{repo}/releases/latest")
    if release.get("draft"):
        raise SystemExit("latest release is a draft; nothing to install")
    if release.get("prerelease"):
        raise SystemExit("latest release is a pre-release; use --channel pre-release")
    print(json.dumps(release))
    raise SystemExit(0)

releases = fetch(f"https://api.github.com/repos/{repo}/releases?per_page=100")
for release in releases:
    if release.get("prerelease") and not release.get("draft"):
        print(json.dumps(release))
        raise SystemExit(0)

raise SystemExit("no pre-release found")
PY
}

pick_linux_asset() {
  local release_json=$1
  local arch=$2

  python3 - "$arch" <<'PY' <<<"$release_json"
import json
import sys

arch = sys.argv[1]
release = json.load(sys.stdin)
tag = release["tag_name"]
version = tag[1:] if tag.startswith("v") else tag
expected = f"vaps-{version}-linux-{arch}.tar.gz"

for asset in release.get("assets", []):
    if asset.get("name") == expected:
        print(json.dumps({
            "tag": tag,
            "version": version,
            "asset_name": expected,
            "download_url": asset["browser_download_url"],
        }))
        raise SystemExit(0)

names = ", ".join(asset.get("name", "?") for asset in release.get("assets", []))
raise SystemExit(f"asset not found: {expected}; available: {names or '(none)'}")
PY
}

download_binary() {
  local install_dir=$1
  local download_url=$2
  local tmpdir

  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' RETURN

  log "downloading $(basename "$download_url")"
  curl -fsSL -L -o "$tmpdir/archive.tar.gz" "$download_url"
  tar -xzf "$tmpdir/archive.tar.gz" -C "$tmpdir"

  local extracted
  extracted=$(find "$tmpdir" -maxdepth 1 -type f -name 'vaps' | head -n 1)
  [ -n "$extracted" ] || die "archive did not contain a vaps binary"

  install -m 0755 "$extracted" "$install_dir/vaps"
}

CLI_CHANNEL=""
PINNED_VERSION=""
RESTART=0

while [ $# -gt 0 ]; do
  case "$1" in
    --channel)
      [ $# -ge 2 ] || die "--channel requires a value"
      CLI_CHANNEL=$2
      shift 2
      ;;
    --version)
      [ $# -ge 2 ] || die "--version requires a value"
      PINNED_VERSION=$2
      shift 2
      ;;
    --restart)
      RESTART=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1 (use --help)"
      ;;
  esac
done

[ -f "$CONF" ] || die "missing $CONF; run install.sh first"
# shellcheck disable=SC1090
source "$CONF"

: "${GITHUB_REPO:?missing GITHUB_REPO in $CONF}"
: "${ARCH:?missing ARCH in $CONF}"
: "${INSTALL_DIR:?missing INSTALL_DIR in $CONF}"

if [ "$INSTALL_DIR" != "$SCRIPT_DIR" ]; then
  die "install dir in $CONF ($INSTALL_DIR) does not match $SCRIPT_DIR"
fi
INSTALL_DIR="$SCRIPT_DIR"

CHANNEL="${CLI_CHANNEL:-${CHANNEL:-release}}"
case "$CHANNEL" in
  release|pre-release) ;;
  *) die "invalid channel: $CHANNEL (expected release or pre-release)" ;;
esac

require_cmd curl
require_cmd tar
require_cmd python3
require_cmd install

release_json=$(resolve_release_json "$GITHUB_REPO" "$CHANNEL" "$PINNED_VERSION")
asset_json=$(pick_linux_asset "$release_json" "$ARCH")
tag=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["tag"])' <<<"$asset_json")
download_url=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["download_url"])' <<<"$asset_json")
asset_name=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["asset_name"])' <<<"$asset_json")

if [ -n "${INSTALLED_TAG:-}" ] && [ "$tag" = "$INSTALLED_TAG" ] && [ -z "$PINNED_VERSION" ]; then
  log "already on $tag; nothing to do"
  exit 0
fi

was_running=0
if [ -x "$START_SCRIPT" ] && "$START_SCRIPT" status >/dev/null 2>&1; then
  was_running=1
  log "stopping running vaps"
  "$START_SCRIPT" stop
fi

log "updating to $tag ($asset_name)"
download_binary "$INSTALL_DIR" "$download_url"

cat >"$CONF" <<EOF
# Generated by scripts/install.sh. Used by update.sh.
GITHUB_REPO="$GITHUB_REPO"
CHANNEL="$CHANNEL"
ARCH="$ARCH"
INSTALL_DIR="$INSTALL_DIR"
INSTALLED_TAG="$tag"
EOF

log "update complete: $tag"

if [ "$RESTART" = 1 ] || [ "$was_running" = 1 ]; then
  "$START_SCRIPT" start
fi
EOF
  chmod 0755 "$install_dir/update.sh"
}

main() {
  parse_args "$@"

  require_cmd curl
  require_cmd tar
  require_cmd python3
  require_cmd install

  local arch
  arch=$(detect_arch)

  log "repo=$GITHUB_REPO channel=$CHANNEL arch=$arch install_dir=$INSTALL_DIR"

  local release_json asset_json tag download_url asset_name
  release_json=$(resolve_release_json "$GITHUB_REPO" "$CHANNEL" "$PINNED_VERSION")
  asset_json=$(pick_linux_asset "$release_json" "$arch")
  tag=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["tag"])' <<<"$asset_json")
  download_url=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["download_url"])' <<<"$asset_json")
  asset_name=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["asset_name"])' <<<"$asset_json")

  log "selected release $tag ($asset_name)"

  mkdir -p "$INSTALL_DIR/data" "$INSTALL_DIR/logs"
  download_binary "$INSTALL_DIR" "$download_url"
  write_default_config "$INSTALL_DIR"
  write_install_conf "$INSTALL_DIR" "$GITHUB_REPO" "$CHANNEL" "$arch" "$tag"
  write_start_script "$INSTALL_DIR"
  write_update_script "$INSTALL_DIR"

  log "installed to $INSTALL_DIR"
  log "  binary:  $INSTALL_DIR/vaps"
  log "  config:  $INSTALL_DIR/config.toml"
  log "  start:   $INSTALL_DIR/start.sh"
  log "  update:  $INSTALL_DIR/update.sh"

  if [ "$START_AFTER_INSTALL" = 1 ]; then
    "$INSTALL_DIR/start.sh" start
  fi
}

main "$@"
