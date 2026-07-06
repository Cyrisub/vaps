# vaps

A Go HTTP server for virtual asset payload storage.

## Development

```sh
make fmt
make vet
make test
make test-functional
make build
make build-legacy-v1
```

## Release

Releases are published automatically when a semantic version tag is pushed to GitHub. Pushes to `dev` run unit and functional tests in the `Test` workflow; release waits for that workflow to succeed on the tagged commit, then builds and uploads cross-platform binaries.

Tag format (SemVer):

```sh
git tag v1.0.0
git push origin v1.0.0
```

Pre-release tags such as `v1.0.0-beta.1` are also supported. Release artifacts are named `vaps-<version>-<os>-<arch>.tar.gz` on Linux and `.zip` on macOS and Windows. Each package includes the binary (`vaps` or `vaps.exe`), default `config.toml`, `start`/`update` helper scripts, and `release-api` helpers. SHA256 checksums in the release notes are for the target binary, not the archive. Run `vaps --version` to inspect the embedded version and release channel.

GitHub also attaches automatic "Source code (zip/tar.gz)" downloads to every release. That behavior is controlled by GitHub and cannot be disabled from the workflow.

Release type is chosen from the branch that contains the tagged commit:

| Branch | GitHub release type |
|--------|---------------------|
| `main` or `master` | Latest (formal release) |
| `dev` | Pre-release |
| Any other branch | Draft |

The release title is the tag (for example `v0.0.1`). Formal releases include a commit summary since the previous tag; every release lists SHA256 checksums for its target binaries so republished builds with the same tag can be compared. Pre-releases and drafts may replace an existing release with the same tag; formal releases reject duplicate tags.

## Installation

Release packages are self-contained: extract and run. Each archive contains:

| File | Linux / macOS | Windows |
|------|---------------|---------|
| Server binary | `vaps` | `vaps.exe` |
| Start helper | `start.sh` | `start.ps1` |
| Update helper | `update.sh` | `update.ps1` |
| Release lookup helper | `release-api.sh` | `release-api.ps1` |
| Default config | `config.toml` | `config.toml` |
| Install metadata | `vaps-install.conf` | `vaps-install.conf` |

`config.toml` is copied from `internal/appconfig/config.toml` at build time and matches the defaults embedded in the binary. Updates preserve an existing `config.toml` and `data/`.

`vaps-install.conf` is generated at build time with `GITHUB_REPO`, `ARCH`, and `GOOS`. `INSTALL_DIR` is filled in on the first `./update.sh` or `update.ps1` run from the directory where the package was extracted.

The binary records its version and release channel at build time. Inspect them with:

```sh
./vaps --version
# vaps 0.0.1-alpha (pre-release)
```

### Install

Release packages are self-contained, so first-time install is extract-and-run. Download from [GitHub Releases](https://github.com/Cyrisub/vaps/releases) (browser download needs no API), or fetch a known tag with a direct asset URL:

```sh
TAG=v0.0.1-alpha
VER=${TAG#v}
INSTALL_DIR=/opt/vaps
ARCH=amd64   # must match the downloaded package

mkdir -p "$INSTALL_DIR"
curl -fsSL -o /tmp/vaps.tgz \
  "https://github.com/Cyrisub/vaps/releases/download/${TAG}/vaps-${VER}-linux-${ARCH}.tar.gz"
tar -xzf /tmp/vaps.tgz -C "$INSTALL_DIR"
cd "$INSTALL_DIR" && ./start.sh start
```

Pick the tag from the releases page for pre-release builds. The URL pattern is `.../releases/download/<tag>/vaps-<version>-<os>-<arch>.tar.gz` with `<version>` equal to the tag without a leading `v`. The archive already includes `vaps-install.conf` (repo/arch/os) and `config.toml`.

### After install

A typical install directory looks like:

```text
/opt/vaps/
  vaps
  start.sh
  update.sh
  release-api.sh
  config.toml
  vaps-install.conf
  data/
  logs/
```

Start, stop, and inspect the background service:

```sh
./start.sh start
./start.sh status
./start.sh stop
./start.sh restart
```

Update to the latest release for the binary's channel (scripts are refreshed from the new package; `config.toml` and `data/` are kept):

```sh
./update.sh
```

Override the update channel or pin a version when needed:

```sh
./update.sh --channel pre-release
./update.sh --version v0.0.1-alpha
./update.sh --restart
```

By default, `update.sh` reads the release channel from `./vaps --version` and only uses `--channel` when you pass it explicitly.

### macOS and Windows

Release `.zip` packages for macOS and Windows include the same helper scripts adapted for each platform. Use them the same way after extracting the archive: edit `config.toml` if needed, run `start.ps1` or `start.sh`, and use `update.ps1` or `update.sh` to upgrade in place.

## Configuration and local development

The default binary is written to `bin/vaps`. The legacy build enables v1 and backup HTTP endpoints via the `vaps_legacy_v1` build tag and is written to `bin/vaps-legacy`.

Run the server with defaults:

```sh
./bin/vaps
```

Or use a TOML config file:

```sh
./bin/vaps --config config.toml
```

Default settings are embedded in the binary from `internal/appconfig/config.toml`. Copy that file as a starting template. Size fields accept plain integers or go-humanize byte sizes such as `64MiB` and `42 MB`. Command-line flags can still override config values. Flag names are derived from TOML paths, for example `-addr`, `-data-dir`, `-cache-bytes`, `-backup-backend`, `-backup-flush-interval`, `-backup-max-pending`, and `-backup-svn-url`. The legacy `-backup-svnmucc-bin` flag is also accepted.

By default, payload blobs are stored under `data/blobs` and metadata is stored in `data/metadata.db`.
Relative paths are resolved from the directory that contains the `vaps` binary, not from the shell's current working directory.
The in-memory LRU cache defaults to 64 MiB total and caches payloads up to 4 MiB.
Access logs are written for every HTTP request. Status logs are written every minute by default; set `status_log_interval` to `"0s"` or use `-status-log-interval 0s` to disable them. Logs are written to stderr and to `logs/vaps-YYYY-MM-DD.log` beside the binary by default.

## Backup

Backup support is optional. Enable the SVN backend with `backup.backend = ["svn"]` and `backup.svn.url` in `config.toml`, or override them from the command line:

```sh
./bin/vaps --config config.toml --backup-backend svn --backup-svn-url https://svn.example.com/repo/vaps-backup
```

Multiple comma-separated values can be passed to `-backup-backend`, for example `-backup-backend svn,none`.

The SVN backend stores payloads under the configured backup root using a three-level hash shard path: `<hash[0:2]>/<hash[2:4]>/<hash[4:6]>/<hash[6:]>.upayload`. On startup, VAPS starts a background refresh of the in-memory backup metadata set; for SVN this is a recursive `svn list` of the configured backup root. Listed backup payloads are merged into the main metadata database as backup-only records when no local record exists. A later `GET /v1/payload` for a backup-only payload downloads it from the backend, writes it into local blobs, and updates the metadata with the local size. Payload uploads are queued as `pending` and flushed in batches when `backup.flush_interval` elapses or `backup.max_pending` is reached, reducing small SVN commits. Set `backup.svn.bin` and `backup.svn.mucc_bin`, or use command-line overrides, to choose different binary names when needed.

## HTTP API

By default, the server exposes v2 endpoints. Legacy v1 payload and backup endpoints are available only in builds compiled with `-tags vaps_legacy_v1`.

Payload objects are identified with the `iohash` query parameter. An `iohash` is the lowercase hex UE `FIoHash` of the payload bytes: BLAKE3-256 truncated to the leading 20 bytes and encoded as 40 hex characters.

### Auth

- `POST /v2/auth/request`

  Request a bearer token. No auth required. If an active token already exists for
  the same `client_id` and client IP, that token is reused and its idle expiry is refreshed.

  Request:

  ```json
  {"client_id":"studio-1","client_info":{"hostname":"dev-box","version":"1.0"}}
  ```

  Response:

  ```json
  {"token":"<token>","expires_at":"2026-07-04T15:16:00Z"}
  ```

  `expires_at` uses a sliding idle window configured by `auth.expire_time`. Any successful
  `Authorization: Bearer <token>` check refreshes the expiry time.

- `GET /v2/auth/expire?token=<token>`

  Immediately revoke a token. No auth header required.

Dashboard auth cleanup (HTML Auth Browser):

- `POST /dashboard/auth/clear-expired` deletes expired tokens from the auth database.
- `POST /dashboard/auth/clear-all` deletes every stored auth token.

### Pull

Requires `Authorization: Bearer <token>`.

- `HEAD /v2/payload/pull?iohash=<iohash>`

  Returns `ETag`, `Content-Length`, and `Accept-Ranges: bytes`.

- `GET /v2/payload/pull?iohash=<iohash>`

  Returns payload bytes. Supports standard single-range requests and responds with `206 Partial Content` when appropriate.

### Push

Requires `Authorization: Bearer <token>`.

The same URL supports direct upload for small payloads and tus uploads for large/resumable payloads.

- `OPTIONS /v2/payload/push`

  Returns tus capabilities and `Upload-Direct-Max-Bytes`.

- `POST /v2/payload/push?iohash=<iohash>` direct upload

  Upload the request body when no tus headers are present. Bodies larger than `upload.direct_max_bytes` return `413 Payload Too Large`.

- `POST /v2/payload/push?iohash=<iohash>` tus create

  Create a tus upload session with `Tus-Resumable: 1.0.0` and `Upload-Length`.

- `HEAD /v2/payload/push?iohash=<iohash>&upload_id=<upload_id>`

  Query the current upload offset.

- `PATCH /v2/payload/push?iohash=<iohash>&upload_id=<upload_id>`

  Append bytes using `Content-Type: application/offset+octet-stream`, `Upload-Offset`, and optional `Upload-Checksum`.

- `DELETE /v2/payload/push?iohash=<iohash>&upload_id=<upload_id>`

  Terminate an incomplete upload session.

Supported tus extensions: `creation`, `creation-with-upload`, `concatenation`, `expiration`, `termination`, `checksum`.

### Metadata

- `POST /v2/metadata/exists`

  Requires bearer token. Batch payload existence check. Successful auth refreshes token idle expiry.

- `POST /v2/metadata`

  No auth. Report VCS association data from a post-commit hook.

- `GET /v2/metadata?payload_hash=<iohash>`

  Query VCS metadata records for a payload.

### Legacy v1 (build tag only)

When built with `-tags vaps_legacy_v1`, the server also exposes:

- `PUT/GET/HEAD /v1/payload?iohash=<iohash>`
- `POST /v1/payload/exists`
- `GET /v1/backup/payloads`
- `PUT /v1/backup/payload?iohash=<iohash>`

### Dashboard

- `GET /health`

  Returns `200 OK` with `ok` as plain text.

- `GET /dashboard`

  Returns a read-only HTML dashboard with server statistics.

- `GET /dashboard/info`

  Returns a read-only HTML page with process, runtime, and configuration summary.

- `GET /dashboard/info.json`

  Returns the same server info as JSON (version/channel, uptime, GOOS/GOARCH, paths, non-secret config).

- `GET /dashboard/stats`

  Returns dashboard statistics as JSON:

  ```json
  {
    "metadata": {"payload_count": 1, "total_bytes": 123, "cached_count": 1},
    "cache": {"entries": 1, "used_bytes": 123, "max_bytes": 67108864, "max_object_bytes": 4194304, "hits": 0, "misses": 0}
  }
  ```

Invalid hashes and malformed JSON return `400 Bad Request`. Unexpected storage or metadata errors return `500 Internal Server Error`.

Optional: add Go-installed tools to your shell PATH if you want to call them directly:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```
