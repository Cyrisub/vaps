#!/usr/bin/env bash
# Remote bootstrap installer. Intended entry:
#   curl -fsSL https://github.com/<repo>/releases/latest/download/install.sh | bash
#   curl -fsSL https://github.com/<repo>/releases/download/<tag>/install.sh | bash -s -- [options]
#
# Placeholders below are filled at release publish time.
set -euo pipefail

GITHUB_REPO="__GITHUB_REPO__"
DEFAULT_CHANNEL="__CHANNEL__"

usage() {
  cat <<'USAGE'
Usage: install.sh [options]

Download and extract a vaps release package into the target directory
(default: current working directory). If vaps is already installed there,
perform an in-place online update without relying on the local update.sh.

Options:
  --dir DIR           Install directory (default: current working directory)
  --channel CHANNEL   release or pre-release (default: baked in at publish time)
  --version VERSION   Install a specific tag, e.g. v1.0.0
  -h, --help          Show this help

Examples:
  curl -fsSL .../install.sh | bash
  curl -fsSL .../install.sh | bash -s -- --dir /opt/vaps
  curl -fsSL .../install.sh | bash -s -- --version v1.0.0
USAGE
}

log() {
  printf '[install] %s\n' "$*"
}

check_s3_credentials() {
  if [ -f "$INSTALL_DIR/.env" ]; then
    log "S3 credentials file found: $INSTALL_DIR/.env (it overrides process environment variables)"
    log "ensure it contains VAPS_STORAGE_S3_SECRETID and VAPS_STORAGE_S3_SECRETKEY"
    return
  fi

  if [ -n "${VAPS_STORAGE_S3_SECRETID:-}" ] && [ -n "${VAPS_STORAGE_S3_SECRETKEY:-}" ]; then
    log "S3 credential environment variables detected"
    return
  fi

  log "warning: S3 credentials were not detected"
  log "set VAPS_STORAGE_S3_SECRETID and VAPS_STORAGE_S3_SECRETKEY, or create $INSTALL_DIR/.env"
}

die() {
  printf '[install] error: %s\n' "$*" >&2
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

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported arch: $(uname -m)" ;;
  esac
}

normalize_tag() {
  case "$1" in
    v*) printf '%s\n' "$1" ;;
    *) printf 'v%s\n' "$1" ;;
  esac
}

archive_name() {
  local tag=$1 goos=$2 arch=$3
  local version=${tag#v}
  if [ "$goos" = "windows" ]; then
    printf 'vaps-%s-%s-%s.zip\n' "$version" "$goos" "$arch"
  else
    printf 'vaps-%s-%s-%s.tar.gz\n' "$version" "$goos" "$arch"
  fi
}

download_url() {
  local repo=$1 tag=$2 goos=$3 arch=$4
  local asset
  asset=$(archive_name "$tag" "$goos" "$arch")
  printf 'https://github.com/%s/releases/download/%s/%s\n' "$repo" "$tag" "$asset"
}

web_latest_tag() {
  local repo=$1 tag
  tag=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
    -H "User-Agent: vaps-installer" \
    "https://github.com/${repo}/releases/latest" \
    | sed 's|.*/||')
  case "$tag" in
    ''|releases)
      return 1
      ;;
  esac
  printf '%s\n' "$tag"
}

parse_html_prerelease_tag() {
  local html=$1
  local line tag="" pending_warning=0

  while IFS= read -r line; do
    case "$line" in
      *'/releases/tag/'*)
        tag=$(sed -n 's|.*releases/tag/\([^"]*\).*|\1|p' <<<"$line" | head -n1)
        pending_warning=0
        ;;
      *'Label--warning'*)
        pending_warning=1
        ;;
      Pre-release*)
        if [ -n "$tag" ] && [ "$pending_warning" = 1 ]; then
          printf '%s\n' "$tag"
          return 0
        fi
        pending_warning=0
        ;;
      *)
        pending_warning=0
        ;;
    esac
  done < <(printf '%s' "$html" | tr '>' '\n')

  return 1
}

web_prerelease_tag() {
  local repo=$1 html
  html=$(curl -fsSL \
    -H "User-Agent: vaps-installer" \
    "https://github.com/${repo}/releases") || return 1
  parse_html_prerelease_tag "$html"
}

resolve_tag() {
  local repo=$1 channel=$2 pinned=$3 tag

  if [ -n "$pinned" ]; then
    normalize_tag "$pinned"
    return 0
  fi

  case "$channel" in
    release)
      tag=$(web_latest_tag "$repo") || die "could not resolve latest release tag"
      printf '%s\n' "$tag"
      ;;
    pre-release|draft)
      tag=$(web_prerelease_tag "$repo") || die "no pre-release found; pass --version TAG to install a specific release"
      printf '%s\n' "$tag"
      ;;
    *)
      die "invalid channel: $channel (expected release or pre-release)"
      ;;
  esac
}

parse_vaps_version() {
  local bin=$1
  local line version channel

  line=$("$bin" --version)
  version=$(sed -n 's/^vaps \([^ ]*\) (.*/\1/p' <<<"$line")
  channel=$(sed -n 's/^vaps [^ ]* (\(.*\))$/\1/p' <<<"$line")
  [ -n "$version" ] && [ -n "$channel" ] || die "could not parse version output: $line"
  VAPS_VERSION=$version
  VAPS_CHANNEL=$channel
}

is_installed() {
  local dir=$1
  [ -x "$dir/vaps" ] && [ -f "$dir/vaps-install.conf" ]
}

write_install_conf() {
  local dir=$1
  cat >"$dir/vaps-install.conf" <<EOF
# Install metadata for update.sh.
GITHUB_REPO="$GITHUB_REPO"
ARCH="$ARCH"
GOOS="$GOOS"
INSTALL_DIR="$dir"
EOF
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
    linux|darwin)
      tar -xzf "$archive_path" -C "$extracted"
      ;;
    *) die "unsupported platform: $goos" ;;
  esac

  [ -f "$extracted/vaps" ] || die "package did not contain vaps binary"
  [ -f "$extracted/start.sh" ] || die "package did not contain start.sh"
  [ -f "$extracted/update.sh" ] || die "package did not contain update.sh"
  [ -f "$extracted/vaps-install.conf" ] || die "package did not contain vaps-install.conf"

  mkdir -p "$install_dir"
  install -m 0755 "$extracted/vaps" "$install_dir/vaps"
  install -m 0755 "$extracted/start.sh" "$install_dir/start.sh"
  install -m 0755 "$extracted/update.sh" "$install_dir/update.sh"

  if [ ! -f "$install_dir/config.toml" ] && [ -f "$extracted/config.toml" ]; then
    install -m 0644 "$extracted/config.toml" "$install_dir/config.toml"
  fi

  rm -rf "$tmpdir"
}

download_and_install() {
  local install_dir=$1
  local url=$2
  local goos=$3
  local tmpdir archive

  tmpdir=$(mktemp -d)
  archive="$tmpdir/package"
  trap 'rm -rf "$tmpdir"' RETURN

  log "downloading $(basename "$url")"
  curl -fsSL -L -o "$archive" "$url"
  extract_package "$install_dir" "$archive" "$goos"
  write_install_conf "$install_dir"
}

# Refuse to run if release placeholders were not substituted.
# Match lingering "__...__" tokens; do not spell the raw placeholders here or
# release-time sed would rewrite this guard into a self-matching pattern.
case "$GITHUB_REPO" in
  ''|*__*) die "install.sh was not published correctly (missing GITHUB_REPO)" ;;
esac
case "$DEFAULT_CHANNEL" in
  ''|*__*) die "install.sh was not published correctly (missing CHANNEL)" ;;
esac

CLI_DIR=""
CLI_CHANNEL=""
PINNED_VERSION=""

while [ $# -gt 0 ]; do
  case "$1" in
    --dir)
      [ $# -ge 2 ] || die "--dir requires a value"
      CLI_DIR=$2
      shift 2
      ;;
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
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1 (use --help)"
      ;;
  esac
done

require_cmd curl
require_cmd install

GOOS=$(detect_goos)
ARCH=$(detect_arch)

if [ -n "$CLI_DIR" ]; then
  INSTALL_DIR=$CLI_DIR
else
  INSTALL_DIR=$(pwd)
fi
# Resolve to absolute path when possible.
if [ -d "$INSTALL_DIR" ]; then
  INSTALL_DIR=$(cd "$INSTALL_DIR" && pwd)
else
  mkdir -p "$INSTALL_DIR"
  INSTALL_DIR=$(cd "$INSTALL_DIR" && pwd)
fi

CHANNEL="${CLI_CHANNEL:-$DEFAULT_CHANNEL}"
case "$CHANNEL" in
  release|pre-release|draft) ;;
  *) die "invalid channel: $CHANNEL (expected release or pre-release)" ;;
esac

BIN="$INSTALL_DIR/vaps"
START_SCRIPT="$INSTALL_DIR/start.sh"
MODE=install
if is_installed "$INSTALL_DIR"; then
  MODE=update
  log "existing install detected in $INSTALL_DIR; performing online update"
else
  log "installing into $INSTALL_DIR"
fi
check_s3_credentials

tag=$(resolve_tag "$GITHUB_REPO" "$CHANNEL" "$PINNED_VERSION")
url=$(download_url "$GITHUB_REPO" "$tag" "$GOOS" "$ARCH")
asset_name=$(archive_name "$tag" "$GOOS" "$ARCH")

if [ "$MODE" = update ] && [ -z "$PINNED_VERSION" ] && [ -x "$BIN" ]; then
  parse_vaps_version "$BIN"
  current_tag=$(normalize_tag "$VAPS_VERSION")
  if [ "$tag" = "$current_tag" ]; then
    log "already on $tag; nothing to do"
    exit 0
  fi
fi

was_running=0
if [ "$MODE" = update ] && [ -x "$START_SCRIPT" ] && "$START_SCRIPT" status >/dev/null 2>&1; then
  was_running=1
  log "stopping running vaps"
  "$START_SCRIPT" stop
fi

log "${MODE}ing $tag ($asset_name)"
download_and_install "$INSTALL_DIR" "$url" "$GOOS"

log "$MODE complete: $tag"
log "start with: $START_SCRIPT start"

if [ "$was_running" = 1 ]; then
  "$START_SCRIPT" start
fi
