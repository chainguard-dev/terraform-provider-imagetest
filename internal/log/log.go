package log

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"github.com/go-logr/logr"
)

// WithCtx returns a context decorated with the non nil *slog.Logger.
func WithCtx(parent context.Context, logger *slog.Logger) context.Context {
	if parent == nil {
		panic("parent context is nil")
	}
	return logr.NewContextWithSlogLogger(parent, logger)
}

// FromCtx returns a slog.Logger from the context, or the default slog.Logger.
// This is heavily adapted from slog-context.
func FromCtx(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}

	l := logr.FromContextAsSlogLogger(ctx)
	if l == nil {
		return slog.Default()
	}
	return l
}

func Info(ctx context.Context, msg string, args ...any) {
	log(ctx, FromCtx(ctx), slog.LevelInfo, msg, args...)
}

func log(ctx context.Context, l *slog.Logger, level slog.Level, msg string, args ...any) {
	if !l.Enabled(ctx, level) {
		return
	}

	var pc uintptr
	var pcs [1]uintptr
	// skip [runtime.Callers, this function, this function's caller]
	runtime.Callers(3, pcs[:])
	pc = pcs[0]

	r := slog.NewRecord(time.Now(), level, msg, pc)
	r.Add(args...)
	_ = l.Handler().Handle(ctx, r)
}
