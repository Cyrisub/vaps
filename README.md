# vaps

A Go HTTP server for virtual asset payload storage.

## Development

```sh
make fmt
make vet
make test
make build
```

The binary is written to `bin/vaps`.

Run the server with defaults:

```sh
./bin/vaps
```

Or use a JSON config file:

```sh
./bin/vaps -config vaps.json
```

Example `vaps.json`:

```json
{
  "addr": ":8588",
  "data_dir": "data",
  "metadata_db": "",
  "log_dir": "logs",
  "log_retention_days": 7,
  "status_log_interval": "1m",
  "cache": {
    "bytes": 67108864,
    "max_object_bytes": 4194304
  },
  "backup": {
    "backend": "svn",
    "flush_interval": "1m",
    "max_pending": 100,
    "svn": {
      "url": "https://svn.example.com/repo/vaps-backup",
      "bin": "svn",
      "mucc_bin": "svnmucc"
    }
  }
}
```

Command-line flags can still override config values. Flag names are derived from JSON paths, for example `-addr`, `-data-dir`, `-cache-bytes`, `-backup-backend`, `-backup-flush-interval`, `-backup-max-pending`, and `-backup-svn-url`. The legacy `-backup-svnmucc-bin` flag is also accepted.

By default, payload blobs are stored under `data/blobs` and metadata is stored in `data/metadata.db`.
Relative paths are resolved from the directory that contains the `vaps` binary, not from the shell's current working directory.
The in-memory LRU cache defaults to 64 MiB total and caches payloads up to 4 MiB.
Access logs are written for every HTTP request. Status logs are written every minute by default; set `status_log_interval` to `"0s"` or use `-status-log-interval 0s` to disable them. Logs are written to stderr and to `logs/vaps-YYYY-MM-DD.log` beside the binary by default.

## Backup

Backup support is optional. Enable the SVN backend with `backup.backend` and `backup.svn.url` in `vaps.json`, or override them from the command line:

```sh
./bin/vaps -config vaps.json -backup-backend svn -backup-svn-url https://svn.example.com/repo/vaps-backup
```

The SVN backend stores payloads under the configured backup root using a three-level hash shard path: `<hash[0:2]>/<hash[2:4]>/<hash[4:6]>/<hash[6:]>.upayload`. On startup, VAPS starts a background refresh of the in-memory backup metadata set; for SVN this is a recursive `svn list` of the configured backup root. Listed backup payloads are merged into the main metadata database as backup-only records when no local record exists. A later `GET /v1/payload` for a backup-only payload downloads it from the backend, writes it into local blobs, and updates the metadata with the local size. Payload uploads are queued as `pending` and flushed in batches when `backup.flush_interval` elapses or `backup.max_pending` is reached, reducing small SVN commits. Set `backup.svn.bin` and `backup.svn.mucc_bin`, or use command-line overrides, to choose different binary names when needed.

## HTTP API

Payload APIs identify objects with the `iohash` query parameter. An `iohash` is the lowercase hex UE `FIoHash` of the payload bytes: BLAKE3-256 truncated to the leading 20 bytes and encoded as 40 hex characters.

- `GET /health`

  Returns `200 OK` with `ok` as plain text.

- `PUT /v1/payload?iohash=<iohash>`

  Stores the request body as a payload. The body hash must match `iohash`.

  Returns `201 Created` for a new payload or `200 OK` when the payload already exists:

  ```json
  {"hash":"<iohash>","size":123,"stored":true}
  ```

- `HEAD /v1/payload?iohash=<iohash>`

  Checks whether a payload exists without returning the body. Existing payloads return `200 OK` with `ETag`, `Content-Length`, and `Content-Type: application/octet-stream` headers. Missing payloads return `404 Not Found`.

- `GET /v1/payload?iohash=<iohash>`

  Returns the raw payload bytes with `Content-Type: application/octet-stream`. Existing payloads return `200 OK`; missing payloads return `404 Not Found`.

- `POST /v1/payload/exists`

  Checks multiple payload hashes at once.

  Request:

  ```json
  {"hashes":["<iohash>","<iohash>"]}
  ```

  Response:

  ```json
  {"items":{"<iohash>":{"exists":true,"size":123},"<iohash>":{"exists":false}}}
  ```

- `GET /v1/backup/payloads`

  Lists payload objects currently visible in the configured backup backend.

- `PUT /v1/backup/payload?iohash=<iohash>`

  Uploads an existing local payload to the configured backup backend.

- `GET /dashboard`

  Returns a read-only HTML dashboard with server statistics.

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
