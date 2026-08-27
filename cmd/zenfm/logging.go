package main

import (
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type diagnosticResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *diagnosticResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *diagnosticResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *diagnosticResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func diagnosticPath(value string) string {
	for _, prefix := range []string{"/api/v1/public/shares/", "/api/v1/files/archive/", "/api/v1/uploads/", "/s/", "/share/"} {
		if strings.HasPrefix(value, prefix) {
			return prefix + "[redacted]"
		}
	}
	return value
}

func diagnosticRemote(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return host
	}
	return value
}

func logRequests(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		response := &diagnosticResponseWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)
		if response.status == 0 {
			response.status = http.StatusOK
		}
		if response.status < http.StatusBadRequest {
			return
		}
		logger.Printf("request completed: remote=%s method=%s path=%s status=%d duration=%s",
			diagnosticRemote(r.RemoteAddr), r.Method, diagnosticPath(r.URL.Path), response.status,
			time.Since(started).Round(time.Millisecond))
	})
}
