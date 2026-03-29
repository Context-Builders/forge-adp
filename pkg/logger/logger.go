// Package logger initialises a JSON-structured slog logger for all Forge
// services and provides helpers for common log patterns.
package logger

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Init sets the default slog logger to a JSON handler writing to stdout.
// Call once in each service's main() before any other logging.
func Init(service string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level(),
	})
	l := slog.New(h).With(slog.String("service", service))
	slog.SetDefault(l)
	return l
}

// level reads the LOG_LEVEL env var (debug/info/warn/error). Defaults to info.
func level() slog.Level {
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithTask returns a child logger with task_id and agent_role attached.
func WithTask(l *slog.Logger, taskID, agentRole string) *slog.Logger {
	return l.With(slog.String("task_id", taskID), slog.String("agent_role", agentRole))
}

// HTTPMiddleware logs every request as a structured JSON line including
// method, path, status code, and duration.
func HTTPMiddleware(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.Info("http request",
			slog.String("service", service),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Duration("duration_ms", time.Since(start).Round(time.Millisecond)),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// contextKey is unexported to prevent collisions with other packages.
type contextKey struct{}

// ContextWithLogger stores a logger in the context for handler-level use.
func ContextWithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// FromContext retrieves the logger from the context, falling back to the default.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
