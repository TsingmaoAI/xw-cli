// Package logger provides structured logging for the xw application.
//
// This package implements a simple but effective logging system with multiple
// log levels and optional debug mode. It provides:
//   - Multiple log levels (DEBUG, INFO, WARN, ERROR, FATAL)
//   - Structured output with timestamps and caller information
//   - Global and instance-based loggers
//   - Thread-safe operations
//   - Color-coded console output (when appropriate)
//
// Example usage:
//
//	logger.Info("Server starting on port %d", 11581)
//	logger.Debug("Configuration loaded: %+v", cfg)
//	logger.Error("Failed to connect: %v", err)
package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Level represents the severity level of a log message
type Level int

const (
	// DebugLevel is for detailed debugging information
	DebugLevel Level = iota

	// InfoLevel is for general informational messages
	InfoLevel

	// WarnLevel is for warning messages
	WarnLevel

	// ErrorLevel is for error messages
	ErrorLevel

	// FatalLevel is for fatal errors that cause program termination
	FatalLevel
)

// String returns the string representation of the log level
func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	case FatalLevel:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// Logger represents a logger instance with configurable output and level
type Logger struct {
	mu          sync.Mutex
	out         io.Writer
	level       Level
	enableDebug bool
	prefix      string
	flags       int
}

var (
	// std is the global default logger
	std = New(os.Stderr, "", log.LstdFlags)
)

// New creates a new Logger instance.
//
// Parameters:
//   - out: The output writer for log messages
//   - prefix: An optional prefix for all log messages
//   - flags: Log flags (see log package constants)
//
// Returns:
//   - A pointer to a configured Logger
//
// Example:
//
//	logger := logger.New(os.Stdout, "[xw] ", log.LstdFlags|log.Lshortfile)
func New(out io.Writer, prefix string, flags int) *Logger {
	return &Logger{
		out:    out,
		level:  InfoLevel,
		prefix: prefix,
		flags:  flags,
	}
}

// SetLevel sets the minimum log level for this logger.
//
// Messages below this level will be discarded.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetDebug enables or disables debug logging.
//
// When enabled, DEBUG level messages are printed.
func (l *Logger) SetDebug(enable bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enableDebug = enable
	if enable {
		l.level = DebugLevel
	}
}

// Output writes a log message at the specified level.
//
// This is the core logging function used by all level-specific functions.
func (l *Logger) Output(level Level, format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Filter by level
	if level < l.level {
		return
	}

	// Skip debug messages if debug is not enabled
	if level == DebugLevel && !l.enableDebug {
		return
	}

	// Build the log message
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf(format, v...)

	// Get caller information
	caller := ""
	if l.flags&(log.Lshortfile|log.Llongfile) != 0 {
		_, file, line, ok := runtime.Caller(2)
		if ok {
			if l.flags&log.Lshortfile != 0 {
				short := file
				for i := len(file) - 1; i > 0; i-- {
					if file[i] == '/' {
						short = file[i+1:]
						break
					}
				}
				file = short
			}
			caller = fmt.Sprintf(" %s:%d", file, line)
		}
	}

	// Format the complete log line
	logLine := fmt.Sprintf("%s [%s]%s %s%s\n",
		timestamp,
		level.String(),
		caller,
		l.prefix,
		message)

	// Write to output
	l.out.Write([]byte(logLine))

	// Fatal errors cause program termination
	if level == FatalLevel {
		os.Exit(1)
	}
}

// Debug logs a debug message (only if debug mode is enabled).
//
// Example:
//
//	logger.Debug("Processing request: %+v", req)
func (l *Logger) Debug(format string, v ...interface{}) {
	l.Output(DebugLevel, format, v...)
}

// Info logs an informational message.
//
// Example:
//
//	logger.Info("Server started on port %d", 11581)
func (l *Logger) Info(format string, v ...interface{}) {
	l.Output(InfoLevel, format, v...)
}

// Warn logs a warning message.
//
// Example:
//
//	logger.Warn("Deprecated API called: %s", apiName)
func (l *Logger) Warn(format string, v ...interface{}) {
	l.Output(WarnLevel, format, v...)
}

// Error logs an error message.
//
// Example:
//
//	logger.Error("Failed to connect to database: %v", err)
func (l *Logger) Error(format string, v ...interface{}) {
	l.Output(ErrorLevel, format, v...)
}

// Fatal logs a fatal error message and terminates the program.
//
// This function does not return.
//
// Example:
//
//	logger.Fatal("Cannot start server: %v", err)
func (l *Logger) Fatal(format string, v ...interface{}) {
	l.Output(FatalLevel, format, v...)
}

// Global logger functions that use the default logger

// SetLevel sets the level for the global logger
func SetLevel(level Level) {
	std.SetLevel(level)
}

// SetDebug enables or disables debug mode for the global logger
func SetDebug(enable bool) {
	std.SetDebug(enable)
}

// Debug logs a debug message using the global logger
func Debug(format string, v ...interface{}) {
	std.Output(DebugLevel, format, v...)
}

// Info logs an informational message using the global logger
func Info(format string, v ...interface{}) {
	std.Output(InfoLevel, format, v...)
}

// Warn logs a warning message using the global logger
func Warn(format string, v ...interface{}) {
	std.Output(WarnLevel, format, v...)
}

// Error logs an error message using the global logger
func Error(format string, v ...interface{}) {
	std.Output(ErrorLevel, format, v...)
}

// Fatal logs a fatal error message and terminates the program
func Fatal(format string, v ...interface{}) {
	std.Output(FatalLevel, format, v...)
}

// ParseLevel converts a string to a log level.
//
// Supported values: "debug", "info", "warn", "error", "fatal"
//
// Returns InfoLevel if the string is not recognized.
func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "debug":
		return DebugLevel
	case "info":
		return InfoLevel
	case "warn", "warning":
		return WarnLevel
	case "error":
		return ErrorLevel
	case "fatal":
		return FatalLevel
	default:
		return InfoLevel
	}
}
