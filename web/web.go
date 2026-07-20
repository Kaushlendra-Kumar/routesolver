// Package web embeds the single-page frontend so the whole application
// ships as one self-contained binary — no separate static file hosting.
package web

import "embed"

// FS holds the frontend assets (currently just index.html). Pass it to
// api.Config.UI to serve the page at GET /.
//
//go:embed index.html
var FS embed.FS
