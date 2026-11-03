// Package docs serves the real Apollo Sandbox interface at /docs/.
// Go embeds only a minimal integration page; the IDE loads from Apollo's CDN.
package docs

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed public/*
var assets embed.FS

func Handler() http.Handler {
	root, err := fs.Sub(assets, "public")
	if err != nil {
		panic("embedded API documentation: " + err.Error())
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		// Apollo's embedded IDE runs in its own iframe, and loads its JavaScript
		// from Apollo's official CDN. Do not authorize arbitrary frame hosts.
		header.Set("Content-Security-Policy", "default-src 'none'; "+
			"script-src 'self' https://embeddable-sandbox.cdn.apollographql.com; "+
			"style-src 'self'; img-src 'self' data:; font-src 'self'; "+
			"connect-src 'self' https://*.apollographql.com; "+
			"frame-src https://*.apollographql.com; "+
			"base-uri 'none'; object-src 'none'; form-action 'none'; frame-ancestors 'none'")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}
