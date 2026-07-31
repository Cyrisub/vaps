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

Pre-release tags such as `v1.0.0-beta.1` are also supported. Release artifacts are named `vaps-<version>-<os>-<arch>.tar.gz` on Linux and macOS, and `.zip` on Windows. Each package includes the binary (`vaps` or `vaps.exe`), default `config.toml`, and `start`/`update` helper scripts. SHA256 checksums in the release notes are for the target binary, not the archive. Run `vaps --version` to inspect the embedded version and release channel.

GitHub also attaches automatic "Source code (zip/tar.gz)" downloads to every release. That behavior is controlled by GitHub and cannot be disabled from the workflow.

Release type is chosen from the branch that contains the tagged commit:

| Branch | GitHub release type |
|--------|---------------------|
| `main` or `master` | Latest (formal release) |
| `dev` | Pre-release |
| Any other branch | Draft |

The release title is the tag (for example `v0.0.1`). Formal releases include a commit summary since the previous tag; every release lists SHA256 checksums for its target binaries so republished builds with the same tag can be compared. Pre-releases and drafts may replace an existing release with the same tag; formal releases reject duplicate tags.

## Installation

Release packages are self-contained: extract and run. Each platform archive contains:

| File | Linux / macOS | Windows |
|------|---------------|---------|
| Server binary | `vaps` | `vaps.exe` |
| Start helper | `start.sh` | `start.ps1` |
| Update helper | `update.sh` | `update.ps1` |
| Default config | `config.toml` | `config.toml` |
| Install metadata | `vaps-install.conf` | `vaps-install.conf` |

Each GitHub release also publishes a standalone Unix `install.sh` bootstrap asset (not inside the archives).

`config.toml` is copied from `internal/appconfig/config.toml` at build time and matches the defaults embedded in the binary. Updates preserve an existing `config.toml` and `data/`.

`vaps-install.conf` is generated at build time with `GITHUB_REPO`, `ARCH`, and `GOOS`. `INSTALL_DIR` is filled in on the first `./update.sh` or `update.ps1` run from the directory where the package was extracted.

The binary records its version and release channel at build time. Inspect them with:

```sh
./vaps --version
# vaps 0.0.1-alpha (pre-release)
```

### Install

Unix bootstrap (release asset; channel is baked in at publish time):

```sh
# Formal release
curl -fsSL https://github.com/Cyrisub/vaps/releases/latest/download/install.sh | bash

# Pre-release / pinned tag
curl -fsSL https://github.com/Cyrisub/vaps/releases/download/v0.0.1-alpha/install.sh | bash

# Custom directory or version
curl -fsSL https://github.com/Cyrisub/vaps/releases/download/v0.0.1-alpha/install.sh | bash -s -- --dir /opt/vaps
curl -fsSL https://github.com/Cyrisub/vaps/releases/download/v0.0.1-alpha/install.sh | bash -s -- --version v0.0.1-alpha
```

Default install directory is the current working directory. If that directory already contains `vaps` and `vaps-install.conf`, `install.sh` performs an online in-place update (useful when the local `update.sh` cannot safely replace itself). It does not auto-start the service.

You can also download a platform archive directly:

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

macOS uses the same `.tar.gz` layout and Unix helper scripts as Linux. Windows `.zip` packages include `start.ps1` / `update.ps1`. After extracting: edit `config.toml` if needed, run `start.ps1` or `start.sh`, and use `update.ps1` or `update.sh` to upgrade in place.

## Configuration and local development

The default binary is written to `bin/vaps`.

Run the server with defaults:

```sh
./bin/vaps
```

Or use a TOML config file:

```sh
./bin/vaps --config config.toml
```

Default settings are embedded in the binary from `internal/appconfig/config.toml`. Copy that file as a starting template. Size fields accept plain integers or go-humanize byte sizes such as `64MiB` and `42 MB`. Command-line flags can still override config values. Flag names are derived from TOML paths, for example `-addr`, `-storage-s3-bucket`, `-storage-s3-region`, and `-storage-local-max-cache-bytes`. The `[dashboard] info_refresh_interval` setting controls how often the Info page refreshes; it defaults to `5s`.

Payloads are durably stored in the configured S3 bucket at `<prefix>/<iohash>`. The local `data/blobs` directory is a bounded, disposable cache; metadata is stored in `data/metadata.db`.
Relative paths are resolved from the directory that contains the `vaps` binary, not from the shell's current working directory.
The in-memory LRU cache defaults to 64 MiB total and caches payloads up to 4 MiB.
Warning and error access logs are written for HTTP requests; successful and redirect access logs are verbose and hidden by default. Status logs are written every minute by default; set `status_log_interval` to `"0s"` or use `-status-log-interval 0s` to disable them. Startup metadata validation is configured under `[metadata]`: `workers = -1` uses the detected physical CPU core count, `workers = 0` forces one worker, and positive values set an explicit worker count; each worker handles one payload request at a time. `timeout` is the per-object S3 `HEAD` request timeout (including connection, response, and SDK retry time), not the whole validation timeout; it defaults to `10s`. `progress_interval` controls progress logs. Logs are written to stderr and to `logs/vaps-YYYY-MM-DD.log` beside the binary by default.

## Storage

S3 is the authoritative payload store. Each upload is verified against the client-provided UE `FIoHash` and a CRC32C (Castagnoli) checksum of the transmitted bytes, then synchronously committed to S3 before VAPS returns success; the resulting S3 ETag is stored in metadata. On startup, VAPS lists the configured prefix with `ListObjectsV2` and validates object presence, key/hash, size, and ETag. Legacy metadata without an ETag performs a one-time `HeadObject` fallback that also records the ETag. Checksum validation runs immediately before a remote pull; local cache hits do not access S3. VAPS exits if validation fails. Credentials are read from `VAPS_STORAGE_S3_SECRETID` and `VAPS_STORAGE_S3_SECRETKEY`; a `.env` file beside the binary overrides those process environment variables. `storage.s3.endpoint` is required and must be an `http` or `https` URL; `storage.s3.force_path_style` supports MinIO and other S3-compatible services.

## HTTP API

Payload, authentication, and VCS metadata APIs are exposed under `/v2`.
Health and dashboard endpoints are outside that namespace.

### Common conventions

- API paths are rooted at `/v2`.
- Requests that require authentication use `Authorization: Bearer <token>`.
- JSON requests should send `Content-Type: application/json`; JSON responses use `Content-Type: application/json`.
- Timestamps are RFC3339 strings in UTC.
- Unless noted otherwise, invalid parameters or request bodies return `400 Bad Request`; missing or invalid credentials return `401 Unauthorized`; missing payloads return `404 Not Found`.

Payload objects are identified with the `iohash` query parameter. An `iohash` is the lowercase hex UE `FIoHash` of the uncompressed payload: BLAKE3-256 truncated to the leading 20 bytes and encoded as 40 hex characters.

The `checksum` query parameter identifies the transmitted file, such as a compressed file. It is the lowercase 8-character hexadecimal CRC32C (Castagnoli) checksum of the exact request bytes. It is independent from the UE `iohash`; clients must calculate it from the compressed bytes before starting an upload.

### Auth

- `POST /v2/auth/request`

  Request a bearer token. No authentication is required. Tokens are reused for
  the same `client_id` and client IP:

  1. If an **active** token exists, reuse it and refresh its idle expiry.
  2. Else if a **parked** token exists, wake it (clear parked state, set a new
     idle expiry) and return the same token value.
  3. Otherwise issue a new token.

  Request body:

  ```json
  {"client_id":"studio-1","client_info":{"hostname":"dev-box","version":"1.0"}}
  ```

  Successful response (`200 OK`):

  ```json
  {"token":"<token>","expires_at":"2026-07-04T15:16:00Z"}
  ```

  `client_id` is required. `client_info` is optional and is a string map for
  client diagnostics. `expires_at` uses a sliding idle window configured by
  `auth.expire_time`. Every successful authenticated request refreshes the
  token expiry.

- `GET /v2/auth/park`

  Park the caller's active token. Requires `Authorization: Bearer <token>`.
  The token cannot authenticate again until a matching `/v2/auth/request` wakes
  it, and it does not idle-expire while parked.

  Successful response (`200 OK`):

  ```json
  {"parked":true}
  ```

- `GET /v2/auth/expire`

  Immediately revoke the caller's token. Requires
  `Authorization: Bearer <token>`. Revoked tokens are never reused.

  Successful response (`200 OK`):

  ```json
  {"expired":true}
  ```

Dashboard auth cleanup (also available from the HTML Auth Browser):

- `POST /dashboard/auth/clear-expired` deletes expired tokens from the auth database.
- `POST /dashboard/auth/clear-all` deletes every stored auth token.

### Payload pull

Requires `Authorization: Bearer <token>`.

- `HEAD /v2/payload/pull?iohash=<iohash>`

  Check whether a payload exists without downloading its body. A successful
  response (`200 OK`) includes `ETag`, `Content-Length`, `Content-Type:
  application/octet-stream`, and `Accept-Ranges: bytes`.

- `GET /v2/payload/pull?iohash=<iohash>`

  Download the exact bytes previously uploaded. A successful full response is
  `200 OK` with `Content-Type: application/octet-stream` and `ETag`.

  To download a single byte range, send a standard header:

  ```http
  Range: bytes=0-1048575
  ```

  The response is `206 Partial Content` and includes `Content-Range`,
  `Content-Length`, and `Accept-Ranges`. Only one range is supported. Verify
  the complete file with its stored `checksum` after a full download; a
  partial response cannot be verified using the complete-file checksum.

### Payload push

Requires `Authorization: Bearer <token>`.

The same endpoint supports direct upload for small files and tus uploads for
large or resumable files. Every push request must provide both `iohash` and
`checksum`. The server validates `checksum` against the transmitted bytes and
uses `iohash` as the payload identity and storage key.

- `OPTIONS /v2/payload/push`

  Discover upload capabilities. A successful response (`204 No Content`)
  includes `Tus-Resumable`, `Tus-Version`, `Tus-Extension`,
  `Tus-Checksum-Algorithm`, and `Upload-Direct-Max-Bytes`. The latter is the
  deployment-specific maximum body size for direct upload.

- `POST /v2/payload/push?iohash=<iohash>&checksum=<checksum>` direct upload

  Use this form when no tus headers are present. Send the compressed file as
  the request body, preferably with `Content-Type: application/octet-stream`.
  Bodies larger than `Upload-Direct-Max-Bytes` return `413 Payload Too Large`.
  A newly stored payload returns `201 Created`; an identical already stored
  payload returns `200 OK`.

  Successful response:

  ```json
  {"hash":"<iohash>","checksum":"<checksum>","size":123456,"stored":true}
  ```

- `POST /v2/payload/push?iohash=<iohash>&checksum=<checksum>` tus create

  Create a resumable upload session with `Tus-Resumable: 1.0.0` and
  `Upload-Length: <total-file-size>`. Send no body for the normal create flow.
  The successful response is `201 Created` and includes a `Location` URL with
  `upload_id`, `Upload-Offset: 0`, `Upload-Length`, `Tus-Resumable`, and
  `Upload-Expires`. Keep the complete `Location` URL for subsequent requests.

- `HEAD /v2/payload/push?iohash=<iohash>&checksum=<checksum>&upload_id=<upload_id>`

  Query a resumable session. Requires `Tus-Resumable: 1.0.0`. The response is
  `200 OK` with `Upload-Offset`, `Upload-Length`, `Tus-Resumable`, and
  `Upload-Expires`.

- `PATCH /v2/payload/push?iohash=<iohash>&checksum=<checksum>&upload_id=<upload_id>`

  Append one contiguous chunk using `Content-Type:
  application/offset+octet-stream` and `Upload-Offset: <current-offset>`.
  `Upload-Checksum` is optional and verifies this chunk only:

  ```http
  Upload-Checksum: sha256 <base64-digest>
  ```

  It supports `sha1` and `sha256`, with standard base64 encoding. This header
  is separate from the required file-level CRC32C `checksum` query parameter.
  If more bytes remain, the response is `204 No Content` with the new
  `Upload-Offset`. When the final chunk completes the file, the server
  validates the complete checksum and returns `201 Created` with the normal
  push JSON response.

- `DELETE /v2/payload/push?iohash=<iohash>&checksum=<checksum>&upload_id=<upload_id>`

  Terminate an incomplete upload session. Requires `Tus-Resumable: 1.0.0`.
  The response is `204 No Content`; temporary upload data is deleted.

#### tus create with upload

The tus `creation-with-upload` form uses the same `POST` URL with
`Tus-Resumable: 1.0.0` and `Upload-Length`, plus the first bytes in the
request body. The body length must not exceed `Upload-Length`. If it
completes the upload, the response is `201 Created` with the normal push JSON
response; otherwise it returns `201 Created` with `Location` and the current
`Upload-Offset`.

#### tus concatenation

To upload chunks independently, create each partial upload with:

```http
POST /v2/payload/push?iohash=<iohash>&checksum=<checksum>
Tus-Resumable: 1.0.0
Upload-Concat: partial
Upload-Length: 0
```

Append data to each returned partial `Location`. After all partial sessions
are complete, create the final upload:

```http
POST /v2/payload/push?iohash=<iohash>&checksum=<checksum>
Tus-Resumable: 1.0.0
Upload-Concat: final; <partial-location-1> <partial-location-2>
```

The server concatenates the partial files in the listed order, verifies the
complete-file CRC32C checksum, stores the result, and returns `201 Created`
with the normal push JSON response.

Supported tus extensions: `creation`, `creation-with-upload`, `concatenation`, `expiration`, `termination`, `checksum`.

### VCS metadata

- `POST /v2/metadata/exists`

  Check multiple payload identities. Requires bearer authentication.

  Request body:

  ```json
  {"hashes":["<iohash-1>","<iohash-2>"]}
  ```

  Successful response (`200 OK`):

  ```json
  {
    "items": {
      "<iohash-1>": {"exists":true,"size":123456},
      "<iohash-2>": {"exists":false}
    }
  }
  ```

- `POST /v2/metadata`

  Report a VCS association for a payload. No authentication is required. The
  request body must include `payload_hash`, `vcs_type`, `repo`, `revision`, and
  `path`; `asset_id` and `metadata` are optional:

  ```json
  {
    "payload_hash":"<iohash>",
    "vcs_type":"git",
    "repo":"ssh://git.example/project.git",
    "revision":"abc123",
    "path":"/Content/Foo.uasset",
    "asset_id":"Foo",
    "metadata":{"branch":"main"}
  }
  ```

  The body is limited to 64 KiB. A successful response is `201 Created` and
  returns the stored record, including `key`, `reported_at`, and `remote_ip`.

- `GET /v2/metadata?payload_hash=<iohash>`

  Return all VCS association records for the payload. No authentication is
  required. A valid payload with no records returns `200 OK` and an empty
  JSON array.

### Health and dashboard

- `GET /health`

  Liveness check. Returns `200 OK` with `ok` as plain text.

- `GET /dashboard`

  Returns the overview HTML dashboard. This endpoint is intended for a browser;
  clients needing machine-readable data should use `/dashboard/stats` and
  `/dashboard/info.json`.

- `GET /dashboard/info`

  Returns a read-only HTML page with process, runtime, and configuration summary.

- `GET /dashboard/info.json`

  Returns the same server info as JSON (version/channel, uptime, GOOS/GOARCH, paths, non-secret config).

- `GET /dashboard/stats`

  Returns server statistics as JSON:

  ```json
  {
    "metadata": {"payload_count": 1, "total_bytes": 123, "cached_count": 1},
    "cache": {"entries": 1, "used_bytes": 123, "max_bytes": 67108864, "max_object_bytes": 4194304, "hits": 0, "misses": 0}
  }
  ```

- `GET /dashboard/metadata`

  Returns the payload metadata HTML page. The page loads its data from
  `/dashboard/metadata/query`.

- `GET /dashboard/metadata/query`

  Query parameters:

  - `q`: case-insensitive `iohash` substring
  - `status`: comma-separated `local`, `cache`, or `backup`
  - `backup`: `all`, `none`, or `backuped`
  - `min_size`, `max_size`: non-negative byte limits
  - `limit`: 1–500, default 100
  - `offset`: non-negative result offset
  - `sort`: `created` or `size`
  - `order`: `asc` or `desc`

  Response:

  ```json
  {
    "items": [{
      "hash":"<iohash>",
      "checksum":"<crc32c>",
      "size":123456,
      "size_human":"120.6 KiB",
      "status":1,
      "status_label":"cached",
      "status_flags":{"local":true,"cache":false,"backup":true},
      "backup":"backuped",
      "vcs_count":1,
      "created_at":"2026-07-30T12:00:00Z",
      "last_accessed_at":null,
      "backuped_at":null
    }],
    "total":1,
    "limit":100,
    "offset":0
  }
  ```

  `status` is `0` for stored in COS/S3 only and `1` for stored in COS/S3 plus
  the local blob cache. `status_flags.local`, `cache`, and `backup` describe
  the current local blob, in-memory cache, and COS/S3 availability.

- `GET /dashboard/sessions`

  Returns the runtime sessions HTML page. Its supporting JSON endpoints are:

  - `GET /dashboard/sessions/query?status=active|ended|all&q=<text>&limit=<n>&offset=<n>&order=newest|oldest`
  - `GET /dashboard/sessions/log?session_id=<id>&level=info|warn|error&q=<text>&limit=<n>&offset=<n>&order=newest|oldest`

- `GET /dashboard/auth`

  Returns the authentication browser HTML page. Its JSON query endpoint is:

  - `GET /dashboard/auth/query?status=active|parked|expired|revoked&client_id=<text>&hash=<prefix>&limit=<n>&offset=<n>`

- `POST /dashboard/auth/clear-expired`

  Deletes expired authentication records and returns `{"deleted":<count>}`.

- `POST /dashboard/auth/clear-all`

  Deletes all authentication records and returns `{"deleted":<count>}`. All
  clients must request new tokens afterward.

- `GET /dashboard/errors`

  Returns the error log HTML page. Its JSON query endpoint is:

  - `GET /dashboard/errors/query?token_hash=<hash>&path=<path>&min_status=<n>&max_status=<n>&limit=<n>&offset=<n>&include_dashboard=true`

  Dashboard requests are excluded by default. `min_status` and `max_status`
  must be at least 400.

- `GET /dashboard/telemetry`

  Returns the telemetry HTML page.

- `GET /dashboard/static/dashboard.css`
- `GET /dashboard/static/dashboard.js`

  Return the shared dashboard static assets.

### Recommended client flow

1. Call `/v2/auth/request` and retain the returned bearer token.
2. Calculate `iohash` from the uncompressed UE payload and CRC32C
   `checksum` from the compressed bytes that will be transmitted.
3. Use direct `POST` when the file is within `Upload-Direct-Max-Bytes`;
   otherwise use the tus create/`PATCH` flow and resume from `HEAD` after a
   connection failure.
4. Treat a `201 Created` or `200 OK` push response as success, and retain the
   returned `size` and `checksum`.
5. Use `GET /v2/payload/pull?iohash=...` to download the compressed bytes and
   verify the complete downloaded file with the stored CRC32C checksum.

Invalid hashes, unsupported query values, and malformed JSON return
`400 Bad Request`. Unexpected storage or metadata errors return `500 Internal Server Error`.

Optional: add Go-installed tools to your shell PATH if you want to call them directly:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```
