package tma

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Фикстуры встроенной ФС: тесты не зависят от реального содержимого dist/
// (оно меняется при каждой сборке web/), но проверяют ту же логику
// маршрутизации, что и в проде.
type fakeFS map[string]string

func (f fakeFS) Open(name string) (fs.File, error) {
	if name == "." {
		return fakeDir{}, nil
	}
	content, ok := f[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &fakeFile{name: name, content: content}, nil
}

type fakeDir struct{}

func (fakeDir) Stat() (fs.FileInfo, error) { return fakeInfo{name: ".", dir: true}, nil }
func (fakeDir) Read([]byte) (int, error)   { return 0, fs.ErrInvalid }
func (fakeDir) Close() error               { return nil }

type fakeFile struct {
	name    string
	content string
	offset  int
}

func (f *fakeFile) Stat() (fs.FileInfo, error) {
	return fakeInfo{name: f.name, size: int64(len(f.content))}, nil
}
func (f *fakeFile) Close() error { return nil }

// Read обязан вернуть io.EOF после исчерпания контента: fs.ReadFile в
// хендлере читает до EOF, и без него буфер растёт бесконечно (OOM).
func (f *fakeFile) Read(p []byte) (int, error) {
	if f.offset >= len(f.content) {
		return 0, io.EOF
	}
	n := copy(p, f.content[f.offset:])
	f.offset += n
	return n, nil
}

type fakeInfo struct {
	name string
	size int64
	dir  bool
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.dir }
func (i fakeInfo) Sys() any           { return nil }

// newTestHandler собирает Handler на фиктивной ФС: embed.FS не подменить,
// поэтому тестируется та же логика через поле fsys.
func newTestHandler(files map[string]string) http.Handler {
	fsys := fakeFS{}
	for name, content := range files {
		fsys["dist/"+name] = content
	}
	return &handler{fsys: fsys}
}

const testIndex = "<!doctype html><div id=root></div>"

var testFiles = map[string]string{
	"index.html":          testIndex,
	"assets/index-abc.js": "console.log('app')",
	"favicon.svg":         "<svg/>",
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestServeIndexAtRoot(t *testing.T) {
	h := newTestHandler(testFiles)

	for _, p := range []string{"/", "/index.html"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", p, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", p, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", p, got)
		}
		if rec.Body.String() != testIndex {
			t.Errorf("GET %s body = %q, want index.html", p, rec.Body.String())
		}
	}
}

func TestSPAFallbackForDeepLinks(t *testing.T) {
	h := newTestHandler(testFiles)

	for _, p := range []string{"/groups", "/groups/123", "/settings", "/app/groups", "/nope/deep"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (SPA fallback)", p, rec.Code)
		}
		if rec.Body.String() != testIndex {
			t.Errorf("GET %s body = %q, want index.html", p, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", p, got)
		}
	}
}

func TestAPIRequestsAreNotIntercepted(t *testing.T) {
	h := newTestHandler(testFiles)

	for _, p := range []string{"/api", "/api/v1/me", "/api/v1/auth/telegram", "/webhook", "/healthz"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (не перехватывать API)", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s вернул index.html вместо 404", p)
		}
	}
}

func TestHashedAssetsAreImmutable(t *testing.T) {
	h := newTestHandler(testFiles)

	rec := get(t, h, "/assets/index-abc.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/index-abc.js = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want immutable", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", got)
	}
	if rec.Body.String() != "console.log('app')" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestRootLevelFilesAreNotImmutable(t *testing.T) {
	h := newTestHandler(testFiles)

	rec := get(t, h, "/favicon.svg")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /favicon.svg = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache для не-хешированного файла", got)
	}
}

func TestPathTraversalIsRejected(t *testing.T) {
	h := newTestHandler(map[string]string{"index.html": testIndex, "secret.txt": "s3cret"})

	for _, p := range []string{"/../secret.txt", "/assets/../../secret.txt", "/..%2fsecret.txt"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (traversal не отдаёт файл и не делает fallback)", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "s3cret") {
			t.Errorf("GET %s выдал файл вне dist", p)
		}
	}
}

// HEAD обязан возвращать те же заголовки, что GET, но без тела: по нему
// клиент решает, переиспользовать ли закэшированный хешированный ассет.
func TestHeadOnAssetHasNoBody(t *testing.T) {
	h := newTestHandler(testFiles)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/assets/index-abc.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /assets/index-abc.js = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want immutable", got)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len("console.log('app')")) {
		t.Errorf("Content-Length = %q, want длину файла", got)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD вернул тело %q, want пусто", rec.Body.String())
	}
}

// Шрифты/картинки бандла должны получать Content-Type даже в alpine-образе,
// где нет /etc/mime.types (эти расширения вне встроенной таблицы mime).
func TestFontAndImageContentTypes(t *testing.T) {
	h := newTestHandler(map[string]string{
		"index.html":             testIndex,
		"assets/font-x.woff2":    "wOF2",
		"assets/font-y.ttf":      "ttf",
		"assets/pic-z.webp":      "RIFF",
		"assets/modal-w.mjs":     "export {}",
		"assets/unknown-q.weird": "?",
	})

	want := map[string]string{
		"/assets/font-x.woff2": "font/woff2",
		"/assets/font-y.ttf":   "font/ttf",
		"/assets/pic-z.webp":   "image/webp",
		"/assets/modal-w.mjs":  "javascript",
	}
	for path, wantCT := range want {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, wantCT) {
			t.Errorf("GET %s Content-Type = %q, want %q", path, got, wantCT)
		}
	}
}

func TestNonGETIsMethodNotAllowed(t *testing.T) {
	h := newTestHandler(testFiles)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/groups", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /groups = %d, want 405", method, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s /groups Allow = %q, want \"GET, HEAD\"", method, got)
		}
	}
}

// Чужие пути отвечают 404 при ЛЮБОМ методе: иначе POST /webhook в
// polling-режиме (обработчик не смонтирован) получал бы 405 от статики и
// маскировал бы ошибку маршрутизации под «метод не поддерживается».
func TestNonGETOnForeignPathsIsNotFound(t *testing.T) {
	h := newTestHandler(testFiles)

	for _, p := range []string{"/api/v1/me", "/webhook", "/healthz"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(method, p, nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404 (не 405)", method, p, rec.Code)
			}
		}
	}
}

// Встроенная в бинарник статика обязана содержать index.html даже на чистом
// клоне: без него go build упадёт на //go:embed, а прод-сборка Dockerfile
// перезапишет плейсхолдер настоящим бандлом.
func TestEmbeddedDistHasIndex(t *testing.T) {
	data, err := fs.ReadFile(distFS, "dist/index.html")
	if err != nil {
		t.Fatalf("dist/index.html не встроен: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("dist/index.html пуст")
	}
	if !strings.Contains(string(data), "<html") {
		t.Fatalf("dist/index.html не похож на HTML: %q", string(data[:min(80, len(data))]))
	}
}

func TestHandlerOverEmbeddedFS(t *testing.T) {
	// Handler() с реальным embed.FS: / → index.html, /api/... → 404.
	h := Handler()

	if rec := get(t, h, "/"); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("GET / = %d, Cache-Control=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get(t, h, "/api/v1/me"); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/me = %d, want 404", rec.Code)
	}
}
