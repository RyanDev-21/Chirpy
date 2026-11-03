// Package schema embeds the goose migrations so the server binary can
// apply them itself.
package schema

import "embed"

//go:embed *.sql
var FS embed.FS
