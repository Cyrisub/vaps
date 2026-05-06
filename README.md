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

Run the server:

```sh
./bin/vaps -addr :8588 -data-dir data
```

By default, payload blobs are stored under `data/blobs` and metadata is stored in `data/metadata.db`.
Use `-metadata-db` to place the metadata database elsewhere.
Relative paths are resolved from the directory that contains the `vaps` binary, not from the shell's current working directory.
The in-memory LRU cache defaults to 64 MiB total and caches payloads up to 4 MiB. Use `-cache-bytes` and `-cache-max-object-bytes` to tune it.
Access logs are written for every HTTP request. Status logs are written every minute by default; use `-status-log-interval` to tune the interval or `-status-log-interval 0` to disable them. Logs are written to stderr and to `logs/vaps-YYYY-MM-DD.log` beside the binary by default. Use `-log-dir` to choose a different log directory and `-log-retention-days` to tune daily log cleanup.

## HTTP API

Payload APIs identify objects with the `iohash` query parameter. An `iohash` is the lowercase hex SHA-1 hash of the payload bytes.

- `GET /health`

  Returns `200 OK` with `ok` as plain text.

- `PUT /v1/payload?iohash=<sha1>`

  Stores the request body as a payload. The body hash must match `iohash`.

  Returns `201 Created` for a new payload or `200 OK` when the payload already exists:

  ```json
  {"hash":"<sha1>","size":123,"stored":true}
  ```

- `HEAD /v1/payload?iohash=<sha1>`

  Checks whether a payload exists without returning the body. Existing payloads return `200 OK` with `ETag`, `Content-Length`, and `Content-Type: application/octet-stream` headers. Missing payloads return `404 Not Found`.

- `GET /v1/payload?iohash=<sha1>`

  Returns the raw payload bytes with `Content-Type: application/octet-stream`. Existing payloads return `200 OK`; missing payloads return `404 Not Found`.

- `POST /v1/payload/exists`

  Checks multiple payload hashes at once.

  Request:

  ```json
  {"hashes":["<sha1>","<sha1>"]}
  ```

  Response:

  ```json
  {"items":{"<sha1>":{"exists":true,"size":123},"<sha1>":{"exists":false}}}
  ```

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
