// Package webui contains the production frontend copied in by the release
// build. The checked-in fallback keeps `go test` and development builds useful.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var assets embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
