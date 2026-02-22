package main

import (
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if reqID == "" {
			reqID = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", reqID)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if recErr := recover(); recErr != nil {
				logger.Error("panic recovered in http handler",
					"request_id", reqID,
					"method", r.Method,
					"path", r.URL.Path,
					"error", recErr,
					"stack", string(debug.Stack()),
				)
				http.Error(rec, "internal server error", http.StatusInternalServerError)
			}

			durationMs := time.Since(start).Milliseconds()
			attrs := []any{
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", durationMs,
				"remote_addr", r.RemoteAddr,
			}
			switch {
			case rec.status >= 500:
				logger.Error("http request completed", attrs...)
			case rec.status >= 400:
				logger.Warn("http request completed", attrs...)
			default:
				logger.Info("http request completed", attrs...)
			}
		}()

		next.ServeHTTP(rec, r)
	})
}
