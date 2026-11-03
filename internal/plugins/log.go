package plugins

import (
	"bytes"
	"io"
	"log/slog"
)

// standardErrorSink is where one run's standard error goes, and the function
// that ends it (a last line without a newline is written then).
func (runner *Runner) standardErrorSink(name string) (io.Writer, func()) {
	lines := &lineLog{logger: runner.logger.With("plugin", name)}
	return lines, lines.flush
}

// skippedEffect notes an effect name this version does not have.
func (runner *Runner) skippedEffect(name, effect string) {
	runner.logger.Info("plugin printed an effect strig does not know; skipped", "plugin", name, "effect", effect)
}

// lineLog turns what a plugin writes into one log record per line. The text
// is an attribute, not the message, so the handler quotes whatever it holds.
// It is used by one run only (exec writes from one goroutine and joins it
// before Run returns), so it needs no lock; the logger itself is safe to share.
type lineLog struct {
	logger  *slog.Logger
	pending []byte
}

func (lines *lineLog) Write(data []byte) (int, error) {
	lines.pending = append(lines.pending, data...)
	for {
		end := bytes.IndexByte(lines.pending, '\n')
		if end < 0 {
			return len(data), nil
		}
		lines.emit(lines.pending[:end])
		lines.pending = lines.pending[end+1:]
	}
}

func (lines *lineLog) flush() {
	if len(lines.pending) > 0 {
		lines.emit(lines.pending)
		lines.pending = nil
	}
}

func (lines *lineLog) emit(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if len(line) == 0 {
		return
	}
	lines.logger.Info("plugin standard error", "line", string(line))
}
