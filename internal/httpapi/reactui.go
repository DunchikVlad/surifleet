// reactui.go — раздача React-фронтенда (web/dist, чанк 14) из-под /app/.
// Ванильный MVP UI выпилен (чанк 30): / — редирект на /app/.
// Если dist не собран (только placeholder) — /app/ отдаёт страницу-заглушку.
package httpapi

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	webui "github.com/surifleet/surifleet/web"
)

// reactNotBuilt — ответ /app/, когда React-сборка отсутствует в бинаре.
const reactNotBuilt = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><title>SuriFleet — React UI не собран</title></head>
<body style="background:#0f141b;color:#dbe4ee;font:14px/1.5 system-ui,sans-serif;padding:40px">
<h1>React UI не встроен в этот бинарь</h1>
<p>Каталог <code>web/dist</code> содержит только placeholder. Сборка:
<code>cd web && npm install && npm run build</code>, затем пересобрать сервер.</p>
</body></html>`

// mountReactUI регистрирует / → /app/ и /app/*: статика из embed web/dist
// с SPA-fallback на index.html (client-side вкладки), либо заглушка,
// если dist не собран.
func mountReactUI(r chi.Router) {
	static, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		panic("reactui embed: " + err.Error())
	}
	index, indexErr := fs.ReadFile(static, "index.html")

	// Корень — сразу на React UI (ванильный MVP UI выпилен, чанк 30).
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/app/", http.StatusMovedPermanently)
	})
	r.Get("/app", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/app/", http.StatusMovedPermanently)
	})
	r.Get("/app/*", func(w http.ResponseWriter, req *http.Request) {
		p := path.Clean("/" + chi.URLParam(req, "*"))[1:]
		// Файл существует в dist (assets, favicon и т.п.) — отдаём как статику.
		// index.html сюда не попадает: отдаём его только через fallback ниже.
		if p != "" && p != "index.html" && !strings.Contains(p, "..") {
			if f, err := static.Open(p); err == nil {
				st, statErr := f.Stat()
				if statErr == nil && !st.IsDir() {
					defer f.Close()
					rs, ok := f.(io.ReadSeeker)
					if !ok {
						writeError(w, http.StatusInternalServerError, CodeNotFound, "статика недоступна", nil)
						return
					}
					http.ServeContent(w, req, st.Name(), st.ModTime(), rs)
					return
				}
				f.Close()
			}
		}
		// SPA-fallback: любой неизвестный путь под /app/ → index.html React.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if indexErr == nil {
			_, _ = w.Write(index)
		} else {
			_, _ = io.WriteString(w, reactNotBuilt)
		}
	})
}
