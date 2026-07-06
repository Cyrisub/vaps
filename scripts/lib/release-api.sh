#!/usr/bin/env bash
# Shared GitHub release helpers for update.sh (bundled as release-api.sh).

release_log() {
  printf '[release] %s\n' "$*" >&2
}

release_die() {
  printf '[release] error: %s\n' "$*" >&2
  exit 1
}

release_normalize_tag() {
  case "$1" in
    v*) printf '%s\n' "$1" ;;
    *) printf 'v%s\n' "$1" ;;
  esac
}

release_archive_name() {
  local tag=$1 goos=$2 arch=$3
  local version=${tag#v}
  if [ "$goos" = "windows" ] || [ "$goos" = "darwin" ]; then
    printf 'vaps-%s-%s-%s.zip\n' "$version" "$goos" "$arch"
  else
    printf 'vaps-%s-%s-%s.tar.gz\n' "$version" "$goos" "$arch"
  fi
}

release_download_url() {
  local repo=$1 tag=$2 goos=$3 arch=$4
  local asset
  asset=$(release_archive_name "$tag" "$goos" "$arch")
  printf 'https://github.com/%s/releases/download/%s/%s\n' "$repo" "$tag" "$asset"
}

release_github_curl() {
  local url=$1
  local auth=()
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  fi
  curl -fsSL \
    -H "Accept: application/vnd.github+json" \
    -H "User-Agent: vaps-installer" \
    "${auth[@]}" \
    "$url"
}

release_web_latest_tag() {
  local repo=$1
  curl -fsSL -o /dev/null -w '%{url_effective}' \
    -H "User-Agent: vaps-installer" \
    "https://github.com/${repo}/releases/latest" \
    | sed 's|.*/||'
}

release_json_field() {
  local json=$1 field=$2
  sed -n "s/.*\"${field}\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" <<<"$json" | head -n1
}

release_json_bool() {
  local json=$1 field=$2
  sed -n "s/.*\"${field}\"[[:space:]]*:[[:space:]]*\\([^,}\\]]*\\).*/\\1/p" <<<"$json" | head -n1
}

release_first_prerelease_tag() {
  local repo=$1 json block tag prerelease draft

  json=$(release_github_curl "https://api.github.com/repos/${repo}/releases?per_page=100" 2>/dev/null) || return 1

  while IFS= read -r block; do
    [ -n "$block" ] || continue
    echo "$block" | grep -q '"prerelease"[[:space:]]*:[[:space:]]*true' || continue
    echo "$block" | grep -q '"draft"[[:space:]]*:[[:space:]]*true' && continue
    tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$block")
    [ -n "$tag" ] || continue
    printf '%s\n' "$tag"
    return 0
  done < <(printf '%s' "$json" | tr '{' '\n')

  return 1
}

# release_resolve_tag REPO CHANNEL PINNED
# Prints the selected release tag to stdout.
release_resolve_tag() {
  local repo=$1 channel=$2 pinned=$3 tag json

  if [ -n "$pinned" ]; then
    release_normalize_tag "$pinned"
    return 0
  fi

  case "$channel" in
    release)
      if json=$(release_github_curl "https://api.github.com/repos/${repo}/releases/latest" 2>/dev/null); then
        if [ "$(release_json_bool "$json" draft)" = "true" ]; then
          release_die "latest release is a draft; nothing to install"
        fi
        if [ "$(release_json_bool "$json" prerelease)" = "true" ]; then
          release_die "latest release is a pre-release; use --channel pre-release"
        fi
        tag=$(release_json_field "$json" tag_name)
        if [ -n "$tag" ]; then
          printf '%s\n' "$tag"
          return 0
        fi
      fi
      release_log "GitHub API unavailable; using web fallback for latest release"
      tag=$(release_web_latest_tag "$repo")
      [ -n "$tag" ] || release_die "could not resolve latest release tag"
      printf '%s\n' "$tag"
      ;;
    pre-release|draft)
      if tag=$(release_first_prerelease_tag "$repo"); then
        printf '%s\n' "$tag"
        return 0
      fi
      release_die "no pre-release found; set GITHUB_TOKEN or pass --version TAG"
      ;;
    *)
      release_die "invalid channel: $channel (expected release or pre-release)"
      ;;
  esac
}

release_parse_vaps_version() {
  local bin=$1
  local line version channel

  line=$("$bin" --version)
  version=$(sed -n 's/^vaps \([^ ]*\) (.*/\1/p' <<<"$line")
  channel=$(sed -n 's/^vaps [^ ]* (\(.*\))$/\1/p' <<<"$line")
  [ -n "$version" ] && [ -n "$channel" ] || release_die "could not parse version output: $line"
  RELEASE_VAPS_VERSION=$version
  RELEASE_VAPS_CHANNEL=$channel
}

release_tag_for_version() {
  local version=$1
  case "$version" in
    v*) printf '%s\n' "$version" ;;
    *) printf 'v%s\n' "$version" ;;
  esac
}
