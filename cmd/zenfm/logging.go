package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type diagnosticListener struct {
	net.Listener
	logger *log.Logger
}

func (l diagnosticListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err == nil {
		l.logger.Printf("connection accepted: remote=%s local=%s", connection.RemoteAddr(), connection.LocalAddr())
	} else if !errors.Is(err, net.ErrClosed) {
		l.logger.Printf("connection accept failed: %v", err)
	}
	return connection, err
}

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
		if !strings.HasPrefix(r.URL.Path, "/api/") && response.status < http.StatusMultipleChoices {
			return
		}
		logger.Printf("request completed: remote=%s method=%s path=%s status=%d duration=%s",
			diagnosticRemote(r.RemoteAddr), r.Method, diagnosticPath(r.URL.Path), response.status,
			time.Since(started).Round(time.Millisecond))
	})
}
