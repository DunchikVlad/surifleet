// webui.go — встроенный Web UI (MVP фазы 2): статика из embed.FS,
// раздаётся тем же HTTP-сервером. Фронтенд — ванильный JS поверх /api/v1.
// Полноценный React-фронтенд (каталог web/) — следующий этап.
package httpapi

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

//go:embed webui
var webuiFS embed.FS

// mountWebUI регистрирует маршруты веб-интерфейса: / → index.html,
// /ui/* — статические ресурсы (app.js, style.css).
func mountWebUI(r chi.Router) {
	static, err := fs.Sub(webuiFS, "webui")
	if err != nil {
		panic("webui embed: " + err.Error())
	}
	// index.html отдаём напрямую: FileServer делает 301 index.html → ./,
	// что зациклило бы корень.
	indexHTML, err := fs.ReadFile(static, "index.html")
	if err != nil {
		panic("webui embed index.html: " + err.Error())
	}
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	serve := http.FileServer(http.FS(static))
	r.Get("/ui/*", func(w http.ResponseWriter, req *http.Request) {
		req.URL.Path = "/" + chi.URLParam(req, "*")
		serve.ServeHTTP(w, req)
	})
}
