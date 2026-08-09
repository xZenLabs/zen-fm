package server

import (
	"io"
	"net/http"
	"time"
)

type progressResponseWriter struct {
	http.ResponseWriter
	timeout time.Duration
	touch   func()
}

func (w *progressResponseWriter) Write(p []byte) (int, error) {
	controller := http.NewResponseController(w.ResponseWriter)
	_ = controller.SetWriteDeadline(time.Now().Add(w.timeout))
	n, err := w.ResponseWriter.Write(p)
	if n > 0 && w.touch != nil {
		w.touch()
	}
	return n, err
}

func (w *progressResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type activityReader struct {
	reader io.Reader
	touch  func()
}

func (r *activityReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.touch != nil {
		r.touch()
	}
	return n, err
}

type activityReaderAt struct {
	reader io.ReaderAt
	touch  func()
}

func (r *activityReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	n, err := r.reader.ReadAt(p, offset)
	if n > 0 && r.touch != nil {
		r.touch()
	}
	return n, err
}
