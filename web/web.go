// Package web carries the frontend into the binary.
//
// The assets are embedded rather than read from disk so that the compiled
// binary is the entire deployment: there is no asset directory to forget to
// copy alongside it and no path that can be right in development and wrong in
// production.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates/*.html static/*
var files embed.FS

// FS returns the embedded frontend.
func FS() fs.FS { return files }
