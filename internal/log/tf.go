package log

import (
	"context"
	"log/slog"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type TFHandler struct {
	attrs  []slog.Attr
	groups []string
}

const subsystem = "imagetest"

// Enabled implements slog.Handler.
func (h *TFHandler) Enabled(_ context.Context, _ slog.Level) bool {
	// Rely on the handler to filter this out, tflog doesn't provide a public API
	// for determining the providers log level :|
	return true
}

// Handle implements slog.Handler.
func (h *TFHandler) Handle(ctx context.Context, record slog.Record) error {
	// This is a bit of a hack, but it's the only way to get the correct
	// source location for the log message.
	//
	// This creates a new tflog subsystem for logging, with the location
	// offset set to 4, which is the number of frames between this function
	// and the actual logging call site. Then we use this subsystem below to log
	// the message to TF's logger.
	ctx = tflog.NewSubsystem(ctx, subsystem, tflog.WithAdditionalLocationOffset(4))

	attrs := make(map[string]any)
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
		tflog.SubsystemDebug(ctx, subsystem, record.Message, attrs)
	case slog.LevelInfo:
		tflog.SubsystemInfo(ctx, subsystem, record.Message, attrs)
	case slog.LevelWarn:
		tflog.SubsystemWarn(ctx, subsystem, record.Message, attrs)
	case slog.LevelError:
		tflog.SubsystemError(ctx, subsystem, record.Message, attrs)
	default:
		// fallback to Info level
		tflog.SubsystemInfo(ctx, subsystem, record.Message, attrs)
	}

	return nil
}

// WithAttrs implements slog.Handler.
func (h *TFHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TFHandler{attrs: append(h.attrs, attrs...)}
}

// WithGroup implements slog.Handler.
func (h *TFHandler) WithGroup(name string) slog.Handler {
	return &TFHandler{
		attrs:  h.attrs,
		groups: append(h.groups, name),
	}
}
