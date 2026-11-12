package panel

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("panel: embedded dist is missing: " + err.Error())
	}
	return sub
}

func Handler() http.Handler {
	return HandlerFor(FS())
}

func HandlerFor(files fs.FS) http.Handler {
	return &handler{files: files}
}

type handler struct {
	files fs.FS
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		h.serveIndex(w, r)
		return
	}

	info, err := fs.Stat(h.files, name)
	if err == nil && !info.IsDir() {
		if strings.HasSuffix(name, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFileFS(w, r, h.files, name)
		return
	}

	if path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}

	h.serveIndex(w, r)
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, h.files, "index.html")
}
