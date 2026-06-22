package logger

import (
	"log/slog"
	"os"
)

// New creates a new slog logger with JSON formatting and stdout output
func New() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return slog.New(handler)
}

// NewDebug creates a new slog logger with debug level
func NewDebug() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	return slog.New(handler)
}
