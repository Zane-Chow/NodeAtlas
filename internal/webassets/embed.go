package webassets

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var assets embed.FS

func FileSystem() fs.FS {
	filesystem, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return filesystem
}
