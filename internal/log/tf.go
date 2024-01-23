package log

import (
	"context"
	"log/slog"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type TFOption struct {
	// log level (default: info)
	Level slog.Leveler
}

var _ slog.Handler = (*TFHandler)(nil)

type TFHandler struct {
	opt    TFOption
	attrs  []slog.Attr
	groups []string
}

func (o TFOption) NewTFHandler() slog.Handler {
	if o.Level == nil {
		o.Level = slog.LevelInfo
	}

	return &TFHandler{
		opt:    o,
		attrs:  []slog.Attr{},
		groups: []string{},
	}
}

// Enabled implements slog.Handler.
func (h *TFHandler) Enabled(ctx context.Context, level slog.Level) bool {
	// Rely on the handler to filter this out, tflog doesn't provide a public API
	// for determining the providers log level :|
	return true
}

// Handle implements slog.Handler.
func (h *TFHandler) Handle(ctx context.Context, record slog.Record) error {
	attrs := map[string]interface{}{}
	for _, attr := range h.attrs {
		attrs[attr.Key] = attr.Value.Any()
	}

	// record level attrs take precedence over handler opt attrs
	record.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any
		attrs[a.Key] = a.Value.Any()
		return true
	})

	switch record.Level {
	case slog.LevelDebug:
		tflog.Debug(ctx, record.Message, attrs)
	case slog.LevelError:
		tflog.Error(ctx, record.Message, attrs)
	case slog.LevelWarn:
		tflog.Warn(ctx, record.Message, attrs)
	default:
		// fallback to Info level
		tflog.Info(ctx, record.Message, attrs)
	}

	return nil
}

// WithAttrs implements slog.Handler.
func (h *TFHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TFHandler{
		opt:   h.opt,
		attrs: attrs,
	}
}

// WithGroup implements slog.Handler.
func (h *TFHandler) WithGroup(name string) slog.Handler {
	return &TFHandler{
		opt:    h.opt,
		attrs:  h.attrs,
		groups: append(h.groups, name),
	}
}
