package membership

import "log/slog"

// slogWriter adapts slog.Logger to io.Writer for memberlist
type slogWriter struct {
	logger *slog.Logger
}

func (w *slogWriter) Write(p []byte) (n int, err error) {
	w.logger.Debug(string(p))
	return len(p), nil
}
