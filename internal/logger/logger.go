package logger

import (
	"io"
	"os"
	"time"

	"github.com/go-errors/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// InitConsoleLogger writes human-readable logs to stderr.
func InitConsoleLogger(debug bool) {
	Init(os.Stderr, false, debug)
}

// Init configures the global logger. Logs always go to w (stderr), never to
// stdout, which is reserved for command results. With jsonLines each log
// entry is one JSON object per line.
func Init(w io.Writer, jsonLines bool, debug bool) {
	// Deployments log from several goroutines; w may not be safe for
	// concurrent use (e.g. a buffer when embedded), so serialise writes.
	w = zerolog.SyncWriter(w)
	if jsonLines {
		log.Logger = zerolog.New(w).With().Timestamp().Logger()
	} else {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: w, TimeFormat: time.RFC822}).With().Timestamp().Logger()
	}
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

// GetErrorDetails is the error text for the log: the message, and the Go
// stack of wrapped errors only with --debug (it means nothing to users).
func GetErrorDetails(err error) string {
	if e, ok := err.(*errors.Error); ok && zerolog.GlobalLevel() <= zerolog.DebugLevel {
		return e.ErrorStack()
	}
	return err.Error()
}
