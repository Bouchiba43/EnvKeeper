// Package logx provides a tiny leveled logger with clean, human-readable
// output suitable for a long-running daemon. It deliberately avoids any
// dependency and never formats file contents — callers log paths and metadata
// only, so secrets are never written to logs.
package logx

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// Level is a logging verbosity level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ParseLevel converts a string such as "debug" or "info" into a Level,
// defaulting to LevelInfo for unrecognised values.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func (l Level) tag() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Logger is a minimal leveled logger writing to an io.Writer.
type Logger struct {
	l     *log.Logger
	level Level
}

// New returns a Logger that writes to stderr at the given minimum level.
func New(level Level) *Logger {
	return NewWith(os.Stderr, level)
}

// NewWith returns a Logger writing to w. It is primarily useful for tests.
func NewWith(w io.Writer, level Level) *Logger {
	return &Logger{
		l:     log.New(w, "", log.LstdFlags),
		level: level,
	}
}

func (lg *Logger) logf(level Level, format string, args ...any) {
	if level < lg.level {
		return
	}
	lg.l.Printf("[%s] %s", level.tag(), fmt.Sprintf(format, args...))
}

// Debugf logs at debug level.
func (lg *Logger) Debugf(format string, args ...any) { lg.logf(LevelDebug, format, args...) }

// Infof logs at info level.
func (lg *Logger) Infof(format string, args ...any) { lg.logf(LevelInfo, format, args...) }

// Warnf logs at warn level.
func (lg *Logger) Warnf(format string, args ...any) { lg.logf(LevelWarn, format, args...) }

// Errorf logs at error level.
func (lg *Logger) Errorf(format string, args ...any) { lg.logf(LevelError, format, args...) }
