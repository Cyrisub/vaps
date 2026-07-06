#!/usr/bin/env bash
set -euo pipefail

GITHUB_TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
export GITHUB_TOKEN

INSTALL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_DIR="$INSTALL_DIR"
CONF="$INSTALL_DIR/vaps-install.conf"
START_SCRIPT="$INSTALL_DIR/start.sh"
UPDATE_SCRIPT="$INSTALL_DIR/update.sh"
RELEASE_API="$INSTALL_DIR/release-api.sh"
BIN="$INSTALL_DIR/vaps"

usage() {
  cat <<'USAGE'
Usage: update.sh [options]

Download the latest release package for this platform and replace bundled
files. Channel defaults to the installed binary unless --channel is set.
Existing config.toml and data are preserved.

Options:
  --channel CHANNEL   release or pre-release
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

detect_goos() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    *) die "unsupported OS: $(uname -s)" ;;
  esac
}

load_install_conf() {
  [ -f "$CONF" ] || die "missing $CONF"
  # shellcheck disable=SC1090
  source "$CONF"

  : "${GITHUB_REPO:?missing GITHUB_REPO in $CONF}"
  : "${ARCH:?missing ARCH in $CONF}"

  GOOS="${GOOS:-$(detect_goos)}"
  INSTALL_DIR="$SCRIPT_DIR"
}

write_install_conf() {
  cat >"$CONF" <<EOF
# Install metadata for update.sh.
GITHUB_REPO="$GITHUB_REPO"
ARCH="$ARCH"
GOOS="$GOOS"
INSTALL_DIR="$INSTALL_DIR"
EOF
}

sync_install_conf() {
  local recorded=""
  if grep -q '^INSTALL_DIR=' "$CONF" 2>/dev/null; then
    recorded=$(grep '^INSTALL_DIR=' "$CONF" | cut -d= -f2- | tr -d '"')
  fi
  if [ "$recorded" != "$INSTALL_DIR" ]; then
    write_install_conf
  fi
}

load_release_api() {
  [ -f "$RELEASE_API" ] || die "missing $RELEASE_API"
  # shellcheck source=release-api.sh
  source "$RELEASE_API"
}

extract_package() {
  local install_dir=$1
  local archive_path=$2
  local goos=$3
  local tmpdir extracted

  tmpdir=$(mktemp -d)
  extracted="$tmpdir/extracted"
  mkdir -p "$extracted"

  case "$goos" in
    linux)
      tar -xzf "$archive_path" -C "$extracted"
      ;;
    darwin)
      require_cmd unzip
      unzip -q "$archive_path" -d "$extracted"
      ;;
    *) die "unsupported platform: $goos" ;;
  esac

  [ -f "$extracted/vaps" ] || die "package did not contain vaps binary"
  [ -f "$extracted/start.sh" ] || die "package did not contain start.sh"
  [ -f "$extracted/update.sh" ] || die "package did not contain update.sh"
  [ -f "$extracted/release-api.sh" ] || die "package did not contain release-api.sh"

  install -m 0755 "$extracted/vaps" "$install_dir/vaps"
  install -m 0755 "$extracted/start.sh" "$install_dir/start.sh"
  install -m 0755 "$extracted/update.sh" "$install_dir/update.sh"
  install -m 0755 "$extracted/release-api.sh" "$install_dir/release-api.sh"

  if [ ! -f "$install_dir/config.toml" ] && [ -f "$extracted/config.toml" ]; then
    install -m 0644 "$extracted/config.toml" "$install_dir/config.toml"
  fi

  rm -rf "$tmpdir"
}

download_and_install_package() {
  local install_dir=$1
  local download_url=$2
  local goos=$3
  local tmpdir archive

  tmpdir=$(mktemp -d)
  archive="$tmpdir/package"
  trap 'rm -rf "$tmpdir"' RETURN

  log "downloading $(basename "$download_url")"
  curl -fsSL -L -o "$archive" "$download_url"
  extract_package "$install_dir" "$archive" "$goos"
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

[ -f "$CONF" ] || die "missing $CONF"
load_install_conf
sync_install_conf

require_cmd curl
require_cmd install
[ -x "$BIN" ] || die "missing vaps binary: $BIN"

load_release_api
release_parse_vaps_version "$BIN"

CHANNEL="${CLI_CHANNEL:-$RELEASE_VAPS_CHANNEL}"
case "$CHANNEL" in
  release|pre-release|draft) ;;
  *) die "invalid channel: $CHANNEL (expected release or pre-release)" ;;
esac

GOOS="${GOOS:-$(detect_goos)}"
tag=$(release_resolve_tag "$GITHUB_REPO" "$CHANNEL" "$PINNED_VERSION")
download_url=$(release_download_url "$GITHUB_REPO" "$tag" "$GOOS" "$ARCH")
asset_name=$(release_archive_name "$tag" "$GOOS" "$ARCH")
current_tag=$(release_tag_for_version "$RELEASE_VAPS_VERSION")

if [ -z "$PINNED_VERSION" ] && [ "$tag" = "$current_tag" ]; then
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
download_and_install_package "$INSTALL_DIR" "$download_url" "$GOOS"

write_install_conf

log "update complete: $tag"

if [ "$RESTART" = 1 ] || [ "$was_running" = 1 ]; then
  "$START_SCRIPT" start
fi
