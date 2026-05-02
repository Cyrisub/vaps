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

Optional: add Go-installed tools to your shell PATH if you want to call them directly:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```
