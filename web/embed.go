// Package web embeds the static frontend so the whole application ships as one
// Go binary and runs as a single process.
package web

import "embed"

// Assets holds the frontend files served at the site root.
//
//go:embed index.html style.css app.js
var Assets embed.FS
