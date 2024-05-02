package log

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"github.com/chainguard-dev/clog"
	slogmulti "github.com/samber/slog-multi"
)

// WithCtx returns a context decorated with the non nil *slog.Logger.
func WithCtx(parent context.Context, logger *slog.Logger) context.Context {
	if parent == nil {
		panic("parent context is nil")
	}

	clogLogger := clog.New(
		slogmulti.Fanout(
			// TODO(mauren): add fanout to file
			clog.NewHandler(logger.Handler()),
		),
	)
	return clog.WithLogger(parent, clogLogger)
}

func Info(ctx context.Context, msg string, args ...any) {
	log(ctx, clog.FromContext(ctx), slog.LevelInfo, msg, args...)
}

func Debug(ctx context.Context, msg string, args ...any) {
	log(ctx, clog.FromContext(ctx), slog.LevelDebug, msg, args...)
}

func Warn(ctx context.Context, msg string, args ...any) {
	log(ctx, clog.FromContext(ctx), slog.LevelWarn, msg, args...)
}

func Error(ctx context.Context, msg string, args ...any) {
	log(ctx, clog.FromContext(ctx), slog.LevelError, msg, args...)
}

func log(ctx context.Context, l *clog.Logger, level slog.Level, msg string, args ...any) {
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
