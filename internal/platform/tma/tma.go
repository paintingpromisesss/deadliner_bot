// Package tma — отдача статики Telegram Mini App из бинарника (спека §5.3).
//
// Сборка web/ попадает в dist/ этого пакета и встраивается в бинарник через
// embed.FS. Handler обслуживает корень сайта: точное совпадение файла,
// хешированные ассеты с immutable-кэшем, SPA-fallback на index.html для всех
// прочих путей. Префикс /api/ не перехватывается — на него Handler отвечает
// 404, чтобы ошибка монтирования не подменяла ответы REST API на HTML.
package tma

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

const (
	// root — поддерево встроенной ФС, где лежит index.html.
	root = "dist"
	// indexFile — точка входа SPA и цель fallback.
	indexFile = "index.html"
	// assetsPrefix — каталог сборки Vite с хешем содержимого в имени файла.
	assetsPrefix = "assets/"
	// cacheImmutable — кэш для ассетов с хешем в имени (спека §5.3).
	cacheImmutable = "public, max-age=31536000, immutable"
	// cacheNoCache — кэш для index.html: ссылки на ассеты меняются между релизами.
	cacheNoCache = "no-cache"
)

// ownedElsewhere — пути, владельцы которых не статика: webhook Telegram и
// health-check. Handler отвечает на них 404 (не index.html), чтобы при
// монтировании на "/" они не превращались в SPA-роуты. Сравнение точное:
// «/webhook-extra» — обычный SPA-путь.
var ownedElsewhere = map[string]bool{
	"/webhook": true,
	"/healthz": true,
}

// Handler возвращает http.Handler со статикой TMA и SPA-fallback.
// Монтируется на "/" после API-маршрутов.
func Handler() http.Handler {
	return &handler{fsys: distFS}
}

type handler struct {
	fsys fs.FS
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Порядок важен: сначала «чужие» пути, и только потом проверка метода.
	// Иначе POST /webhook в polling-режиме (обработчик не смонтирован) получал
	// бы 405 от статики вместо честного 404, маскируя ошибку маршрутизации.
	if isAPIPath(r.URL.Path) || ownedElsewhere[r.URL.Path] {
		http.NotFound(w, r)
		return
	}

	name, ok := cleanPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Статика TMA отдаётся только на чтение.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if name == "" || name == indexFile {
		h.serveIndex(w, r)
		return
	}

	data, err := fs.ReadFile(h.fsys, path.Join(root, name))
	if err != nil {
		// Неизвестный путь — SPA-роут (например /groups или /groups/123):
		// отдаём index.html, роутер разберёт адрес из location.hash.
		h.serveIndex(w, r)
		return
	}

	cache := cacheNoCache
	if strings.HasPrefix(name, assetsPrefix) {
		cache = cacheImmutable
	}
	writeFile(w, r, data, contentType(name), cache)
}

// serveIndex отдаёт index.html с запретом кэширования.
func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(h.fsys, path.Join(root, indexFile))
	if err != nil {
		http.Error(w, "index.html is not embedded", http.StatusInternalServerError)
		return
	}
	writeFile(w, r, data, "text/html; charset=utf-8", cacheNoCache)
}

// writeFile пишет тело с указанными заголовками (HEAD — без тела).
func writeFile(w http.ResponseWriter, r *http.Request, data []byte, contentType, cache string) {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

// isAPIPath сообщает, что путь обслуживает REST API, а не статика.
func isAPIPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/")
}

// cleanPath нормализует URL-путь в относительное имя файла внутри dist.
// Второе значение — false для путей, которые заведомо не файлы (выход за
// пределы dist, попытка traversal).
func cleanPath(urlPath string) (string, bool) {
	trimmed := strings.TrimPrefix(urlPath, "/")
	if trimmed == "" {
		return "", true
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	return cleaned, true
}

// extraTypes — расширения, которых нет во встроенной таблице mime (проверено
// по Go 1.26: .webp есть, .woff2/.ttf/.otf/.eot — нет); в alpine-образе нет
// /etc/mime.types, поэтому TypeByExtension вернул бы пусто. Ассеты Vite
// хешированы — промах MIME кэшировался бы на год.
var extraTypes = map[string]string{
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".eot":   "application/vnd.ms-fontobject",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".mjs":   "text/javascript; charset=utf-8",
}

// contentType подбирает MIME по расширению; для неизвестных — пусто
// (браузер разберётся по содержимому).
func contentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := extraTypes[ext]; ok {
		return t
	}
	return mime.TypeByExtension(ext)
}
