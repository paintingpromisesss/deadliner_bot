package api

import (
	"net/http"
	"time"
)

// NewServer creates an HTTP server that serves static web and a health endpoint.
func NewServer(addr string, readTimeout, writeTimeout time.Duration, webDir string) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	return &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}
