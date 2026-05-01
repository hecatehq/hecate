package api

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	hecate "github.com/hecate/agent-runtime"
)

// staticUIHandler returns a handler backed by the package-default embedded UI
// (the bundle baked in at build time via //go:embed). Production code uses
// this; tests should use staticUIHandlerFromFS to inject a controlled FS.
func staticUIHandler() http.Handler {
	return staticUIHandlerFromFS(hecate.UISubFS())
}

// staticUIHandlerFromFS serves a React SPA from the supplied filesystem.
// Splitting this out from the embed lets us drive deterministic tests
// (controlled FS, controlled "missing index.html" cases) without depending on
// whether a real UI build happens to be on disk during `go test`.
//
// Behavior:
//   - exact-match files are served via http.ServeContent (Content-Type from
//     the extension, ETag-shaped caching headers).
//   - any path that doesn't resolve to a file falls back to index.html so
//     client-side routes work without server-side config.
//   - if uiFS is nil or has no index.html (e.g. only the .gitkeep
//     placeholder is embedded), every path serves a "UI not built" page.
func staticUIHandlerFromFS(uiFS fs.FS) http.Handler {
	hasUI := uiFS != nil && indexExists(uiFS)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasUI {
			writeUIFallback(w, http.StatusOK)
			return
		}

		urlPath := strings.TrimPrefix(r.URL.Path, "/")
		if urlPath == "" {
			urlPath = "index.html"
		}
		// Reject directory traversal up front. We're not using
		// http.FileServer (we want explicit SPA fallback control), so we
		// guard this ourselves rather than relying on FileServer's cleanup.
		if strings.Contains(urlPath, "..") {
			http.NotFound(w, r)
			return
		}

		file, err := openOrIndex(uiFS, urlPath)
		if err != nil {
			http.Error(w, "ui asset error", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		stat, err := file.Stat()
		if err != nil {
			http.Error(w, "ui asset error", http.StatusInternalServerError)
			return
		}
		if stat.IsDir() {
			file.Close()
			file, err = uiFS.Open("index.html")
			if err != nil {
				writeUIFallback(w, http.StatusOK)
				return
			}
			defer file.Close()
			stat, err = file.Stat()
			if err != nil {
				http.Error(w, "ui asset error", http.StatusInternalServerError)
				return
			}
			urlPath = "index.html"
		}

		seeker, ok := file.(io.ReadSeeker)
		if !ok {
			http.Error(w, "ui asset error", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, path.Base(urlPath), stat.ModTime(), seeker)
	})
}

// openOrIndex opens the requested path, falling back to index.html when the
// path doesn't exist. The fallback is what makes SPA routing work — client
// routes like /admin or /providers don't correspond to embedded files but
// still need to load the React shell.
func openOrIndex(uiFS fs.FS, urlPath string) (fs.File, error) {
	file, err := uiFS.Open(urlPath)
	if err == nil {
		return file, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return uiFS.Open("index.html")
}

func indexExists(fsys fs.FS) bool {
	_, err := fs.Stat(fsys, "index.html")
	return err == nil
}

const uiFallbackHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Hecate — UI not built</title>
<style>
body { font-family: system-ui, -apple-system, sans-serif; max-width: 40rem; margin: 4rem auto; padding: 0 1rem; color: #1f2937; line-height: 1.5; }
h1 { font-size: 1.5rem; }
code { background: #f3f4f6; padding: 0.1rem 0.3rem; border-radius: 0.25rem; }
pre { background: #f3f4f6; padding: 1rem; border-radius: 0.5rem; overflow-x: auto; }
.note { color: #6b7280; font-size: 0.875rem; }
</style>
</head>
<body>
<h1>Hecate is running, but the UI bundle wasn't embedded.</h1>
<p>The gateway API is fully functional — try
<a href="/healthz"><code>GET /healthz</code></a> or
<a href="/v1/models"><code>GET /v1/models</code></a>. To enable this dashboard,
build the UI before the gateway:</p>
<pre>make ui-install
make ui-build
go build -o gateway ./cmd/gateway</pre>
<p class="note">For UI-only iteration, run the dev server:
<code>make ui-dev</code> opens the React app on
<code>http://127.0.0.1:5173</code> with API calls proxied to this gateway.</p>
</body>
</html>
`

func writeUIFallback(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(uiFallbackHTML))
}
