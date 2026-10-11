// Package web embeds the built frontends into the binary: the widget
// (embed.js, the iframe app and the login popup's script) and the reader
// center and site console. Each dist directory holds a committed .gitkeep,
// so the Go build works before or without the frontend builds; the server
// then answers the frontend routes with a notice instead.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:widget/dist
var widget embed.FS

//go:embed all:console/dist
var console embed.FS

// Widget is web/widget/dist.
func Widget() fs.FS { return sub(widget, "widget/dist") }

// Console is web/console/dist.
func Console() fs.FS { return sub(console, "console/dist") }

func sub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
