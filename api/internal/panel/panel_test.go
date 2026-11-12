package panel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFiles() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte("<!doctype html><div id=\"root\"></div>")},
		"assets/app.js":  &fstest.MapFile{Data: []byte("console.log('mise')")},
		"assets/app.css": &fstest.MapFile{Data: []byte("body{margin:0}")},
	}
}

func request(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func TestServesIndexAndClientRoutes(t *testing.T) {
	handler := HandlerFor(testFiles())

	for _, target := range []string{"/", "/board", "/tasks/MI-1"} {
		recorder := request(t, handler, http.MethodGet, target)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", target, recorder.Code)
		}
		if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
			t.Errorf("GET %s content-type = %q, want HTML", target, got)
		}
		body, err := io.ReadAll(recorder.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !strings.Contains(string(body), "id=\"root\"") {
			t.Errorf("GET %s did not serve index.html: %q", target, body)
		}
	}
}

func TestServesAssetsAndMisses(t *testing.T) {
	handler := HandlerFor(testFiles())

	recorder := request(t, handler, http.MethodGet, "/assets/app.js")
	if recorder.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("asset content-type = %q, want JavaScript", got)
	}
	if got := recorder.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("asset cache-control = %q, want immutable", got)
	}

	recorder = request(t, handler, http.MethodGet, "/assets/missing.js")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", recorder.Code)
	}

	recorder = request(t, handler, http.MethodPost, "/")
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", recorder.Code)
	}
}

func TestIndexHTMLIsNotImmutable(t *testing.T) {
	handler := HandlerFor(testFiles())

	recorder := request(t, handler, http.MethodGet, "/index.html")
	if got := recorder.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("index.html cache-control = %q, want a revalidating header", got)
	}
}
