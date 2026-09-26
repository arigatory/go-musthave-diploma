package handler

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Logging returns a middleware that logs every request with its method,
// URI, response status, response size and duration.
func Logging(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.Info("request",
				zap.String("method", r.Method),
				zap.String("uri", r.RequestURI),
				zap.Int("status", rec.status),
				zap.Int("size", rec.size),
				zap.Duration("duration", time.Since(start)),
			)
		})
	}
}

// responseRecorder remembers the response status and the number of bytes written.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	size        int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	n, err := r.ResponseWriter.Write(b)
	r.size += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Recoverer returns a middleware that turns a panic in a handler into
// 500 Internal Server Error and logs it with the stack trace.
func Recoverer(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				log.Error("panic", zap.Any("panic", rec), zap.Stack("stack"))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Decompress is a middleware that transparently decompresses request
// bodies sent with "Content-Encoding: gzip". Malformed gzip bodies are
// rejected with 400 Bad Request.
func Decompress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(strings.ToLower(r.Header.Get("Content-Encoding")), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "invalid gzip body", http.StatusBadRequest)
			return
		}
		defer zr.Close()
		r.Body = readCloser{Reader: zr, closer: r.Body}
		r.Header.Del("Content-Encoding")
		r.Header.Del("Content-Length")
		r.ContentLength = -1
		next.ServeHTTP(w, r)
	})
}

type readCloser struct {
	io.Reader
	closer io.Closer
}

func (rc readCloser) Close() error {
	return rc.closer.Close()
}

// compressibleTypes are the response content types Compress gzips.
var compressibleTypes = []string{"application/json", "text/plain"}

// Compress is a middleware that gzips JSON and plain text responses for
// clients sending "Accept-Encoding: gzip".
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// gzipResponseWriter decides whether to compress when the status is
// written, because only then the handler has set the Content-Type.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (g *gzipResponseWriter) WriteHeader(status int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	h := g.Header()
	if hasBody(status) && h.Get("Content-Encoding") == "" && compressible(h.Get("Content-Type")) {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		g.gz = gzip.NewWriter(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return g.ResponseWriter
}

func (g *gzipResponseWriter) close() {
	if g.gz != nil {
		_ = g.gz.Close()
	}
}

func hasBody(status int) bool {
	return status >= http.StatusOK && status != http.StatusNoContent && status != http.StatusNotModified
}

func compressible(contentType string) bool {
	for _, t := range compressibleTypes {
		if strings.HasPrefix(contentType, t) {
			return true
		}
	}
	return false
}
