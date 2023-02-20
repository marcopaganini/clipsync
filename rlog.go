// This file is part of clipsync (C)2023 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.
// rlog.go - Helper functions for the rlog library.

package main

import (
	"os"

	log "github.com/romana/rlog"
)

// Define a logger object that logs everything using rlog.Debug.
// This is used by mqtt log levels.
type rlogger struct{}

func (rlogger) Println(v ...interface{}) {
	log.Debug(v...)
}
func (rlogger) Printf(format string, v ...interface{}) {
	log.Debugf(format, v...)
}

// fatal logs a message with rlog.Critical and exits with a return code.
func fatal(v ...any) {
	if v != nil {
		log.Critical(v...)
	}
	os.Exit(1)
}

// fatalf logs a message with rlog.Criticalf and exits with a return code.
func fatalf(f string, v ...any) {
	if v != nil {
		log.Criticalf(f, v...)
	}
	os.Exit(1)
}

// setupLogging configures the logging parameters from the command line
// options and other conditions.
func setupLogging(cfg globalConfig) {
	// If stdout does not point to a tty, assume we're using syslog/journald
	// and remove the timestamp, since those systems already add it.
	if fi, _ := os.Stdout.Stat(); (fi.Mode() & os.ModeCharDevice) == 0 {
		os.Setenv("RLOG_LOG_NOTIME", "yes")
	}

	if *cfg.verbose {
		os.Setenv("RLOG_LOG_LEVEL", "DEBUG")
		os.Setenv("RLOG_CALLER_INFO", "yes")
	}
	log.UpdateEnv()
}
