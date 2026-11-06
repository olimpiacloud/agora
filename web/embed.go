package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func FS() fs.FS {
	f, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	return f
}
