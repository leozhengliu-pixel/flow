# Metadata file map

The metadata filename migration is complete. The rename commits group the
source and test files by domain within their existing Go packages.

Embedded consumer registrations live in
[`api/internal/store/metadata_inventory`](../../api/internal/store/metadata_inventory).
The `server/` and `store/` subdirectories mirror the Go source basenames in
`api/cmd/server` and `api/internal/store`. Each registration's `source` names
the corresponding Go file.
