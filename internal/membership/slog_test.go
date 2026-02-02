package membership

import (
	"log/slog"
	"os"
	"testing"
)

func TestSlogWriter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	writer := &slogWriter{logger: logger}

	n, err := writer.Write([]byte("test message"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 12 {
		t.Errorf("expected 12 bytes written, got %d", n)
	}
}
