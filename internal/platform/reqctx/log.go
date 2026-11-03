package reqctx

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// LogHandler adds req_id and user_id from the context to every record
// logged with a *Context method (InfoContext, ErrorContext, ...).
type LogHandler struct{ slog.Handler }

func (h LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if i := infoFrom(ctx); i != nil {
		if i.RequestID != "" {
			r.AddAttrs(slog.String("req_id", i.RequestID))
		}
		if id, ok := i.UserID(); ok {
			r.AddAttrs(slog.String("user_id", id.String()))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h LogHandler) WithAttrs(a []slog.Attr) slog.Handler { return LogHandler{h.Handler.WithAttrs(a)} }
func (h LogHandler) WithGroup(n string) slog.Handler      { return LogHandler{h.Handler.WithGroup(n)} }

// NewLogger builds the process logger. format is "json" (default) or "text";
// level is debug|info|warn|error (default info).
func NewLogger(w io.Writer, format, level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler = slog.NewJSONHandler(w, opts)
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(LogHandler{h})
}
