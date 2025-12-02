package logutil

import (
	"context"
	"log/slog"
)

type contextKey struct{}

// SloggerInto returns a new context with a *slog.Logger stored in it.
func SloggerInto(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, log)
}

// SloggerFrom returns a *slog.Logger from the context.
func SloggerFrom(ctx context.Context) *slog.Logger {
	v := ctx.Value(contextKey{})
	if v == nil {
		return slog.New(slog.DiscardHandler)
	}

	switch v := v.(type) {
	case *slog.Logger:
		return v
	default:
		return slog.New(slog.DiscardHandler)
	}
}
