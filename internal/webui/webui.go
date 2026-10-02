// Package webui embeds the built frontend. A fresh checkout contains only a
// placeholder; `make web-assets` (and the image build) fills build/ with the
// compiled SvelteKit output and its precompressed siblings.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var embedded embed.FS

// FS is the built frontend, rooted at the build directory.
var FS = mustSub(embedded, "build")

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
