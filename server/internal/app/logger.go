package app

import (
	"context"
	"io"
	"log/slog"

	"github.com/go-chi/chi/v5/middleware"
)

// NewLogger returns the JSON logger. Records logged with a request context
// carry a request_id attribute. Never log emails or phone numbers.
func NewLogger(w io.Writer, debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(requestIDHandler{slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})})
}

type requestIDHandler struct{ slog.Handler }

func (h requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := middleware.GetReqID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{h.Handler.WithAttrs(attrs)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{h.Handler.WithGroup(name)}
}
