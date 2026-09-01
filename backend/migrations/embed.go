package migrations

import "embed"

// Files contains the ordered SQL migrations shipped with the server.
//
//go:embed *.sql
var Files embed.FS
