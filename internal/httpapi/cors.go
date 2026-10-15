package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !allowOrigin(origin) {
			next.ServeHTTP(w, r)
			return
		}
		header := w.Header()
		header.Set("Access-Control-Allow-Origin", origin)
		header.Add("Vary", "Origin")
		header.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		header.Set("Access-Control-Allow-Headers", "Authorization, Access-Token, Content-Type, Accept, X-Requested-With")
		header.Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := parsed.Hostname()
	if host == "127.0.0.1" || host == "localhost" {
		return true
	}
	return host == "d3m0.site" || strings.HasSuffix(host, ".d3m0.site")
}
