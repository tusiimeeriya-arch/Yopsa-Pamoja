// Package webui embeds the Yopsa Pamoja web client: a dependency-free
// single-page application served by the API. It talks to the same JSON API the
// Flutter client uses, so no business rule lives in the browser.
package webui

import (
	"embed"
	"path"
)

//go:embed assets
var files embed.FS

var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".svg":   "image/svg+xml",
	".json":  "application/json; charset=utf-8",
	".png":   "image/png",
	".woff2": "font/woff2",
}

// Index returns the application shell.
func Index() []byte {
	body, _ := files.ReadFile("assets/index.html")
	return body
}

// Asset returns one static file from the embedded client.
func Asset(name string) ([]byte, string, bool) {
	if name == "" || name == "/" {
		return nil, "", false
	}
	body, err := files.ReadFile("assets/" + path.Clean(name))
	if err != nil {
		return nil, "", false
	}
	contentType := contentTypes[path.Ext(name)]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return body, contentType, true
}
