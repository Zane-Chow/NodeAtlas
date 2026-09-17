package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

type Dependencies struct {
	Assets fs.FS
}

func NewRouter(deps Dependencies) http.Handler {
	router := chi.NewRouter()
	router.Get("/health/live", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]string{"status": "ok"})
	})
	router.Handle("/*", frontendHandler(deps.Assets))
	return router
}

func frontendHandler(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cleanPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if request.Method != http.MethodGet || hasReservedPrefix(request.URL.Path) {
			http.NotFound(response, request)
			return
		}
		if cleanPath != "." {
			if _, err := fs.Stat(assets, cleanPath); err == nil {
				files.ServeHTTP(response, request)
				return
			}
		}
		if path.Ext(cleanPath) != "" {
			http.NotFound(response, request)
			return
		}
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(index)
	})
}

func hasReservedPrefix(requestPath string) bool {
	for _, prefix := range []string{"/api", "/health", "/ws"} {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}
