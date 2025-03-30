package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

// Level represents the severity level of a log message
type Level int

const (
	// Debug level for detailed information
	Debug Level = iota
	// Info level for general operational information
	Info
	// Warn level for warning conditions
	Warn
	// Error level for error conditions
	Error
	// Fatal level for fatal conditions
	Fatal
)

var levelNames = map[Level]string{
	Debug: "DEBUG",
	Info:  "INFO",
	Warn:  "WARN",
	Error: "ERROR",
	Fatal: "FATAL",
}

// Logger is a simple structured logger
type Logger struct {
	level  Level
	logger *log.Logger
}

// New creates a new logger with the specified level
func New(level Level) *Logger {
	return &Logger{
		level:  level,
		logger: log.New(os.Stdout, "", 0),
	}
}

// SetLevel changes the logger's level
func (l *Logger) SetLevel(level Level) {
	l.level = level
}

// log logs a message at the specified level
func (l *Logger) log(level Level, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	levelName := levelNames[level]
	message := fmt.Sprintf(format, args...)

	l.logger.Printf("[%s] %s: %s", timestamp, levelName, message)

	if level == Fatal {
		os.Exit(1)
	}
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(Debug, format, args...)
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(Info, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(Warn, format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(Error, format, args...)
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(format string, args ...interface{}) {
	l.log(Fatal, format, args...)
}
