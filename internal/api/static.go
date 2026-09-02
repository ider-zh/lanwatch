package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/ider-zh/lanwatch/web"
)

// staticHandler 提供嵌入式前端（SPA）。非 /api 路径：命中文件则直接返回，
// 否则回退到 index.html（支持浏览器直接访问 /timeline、/devices 等前端路由）。
func (a *API) staticHandler() http.Handler {
	sub, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		a.log.Error("embed fs sub", "err", err)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "static assets unavailable", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			// SPA 回退：非 /api 的 GET（含浏览器直访 /devices、/timeline 等前端路由）
			// 一律回退到 index.html，由前端路由接管，不依赖 Accept 头。
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				r.URL.Path = "/"
				fileServer.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
