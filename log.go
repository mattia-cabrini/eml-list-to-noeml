// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// Leveled logging into the system log.
//
// A line has to reach the system log whatever the state of the machine: there
// is nowhere else to look, since a run started by cron has no terminal and
// nobody reads its standard error. There are therefore two ways in, tried in
// turn: the syslog library, which writes to the log socket itself, and
// logger(1), the system's own tool, which both Ubuntu and FreeBSD have and
// which knows how to reach a log the library could not open. Standard error is
// what is left when even that fails, and the cron job pipes it into logger so
// that it lands in the log all the same.

package main

import (
	"fmt"
	"log/syslog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The levels, from the most to the least severe.
const (
	FATAL = iota
	ERROR
	WARNING
	INFO
	DEBUG
)

// defaultLogLevel is what a service configuration without log_level means:
// everything but the debugging details.
const defaultLogLevel = INFO

var levelPrefix = [...]string{
	FATAL:   "[ FATAL   ]",
	ERROR:   "[ ERROR   ]",
	WARNING: "[ WARNING ]",
	INFO:    "[ INFO    ]",
	DEBUG:   "[ DEBUG   ]",
}

// logLevel is the least severe level that still gets written. The service
// configuration sets it as soon as it is read; until then, and for everything
// that goes wrong before that, the default is in force.
var logLevel = defaultLogLevel

// severityName is the syslog severity each level goes in under, spelled as
// logger(1) wants it; the library names the same ones with its own methods.
//
// INFO goes in as a notice and not as an informational message on purpose:
// FreeBSD writes daemon.notice and above to /var/log/messages by default, and
// what this program says at INFO is what the operator has to be able to read
// there. DEBUG stays below that line, where it belongs.
var severityName = [...]string{
	FATAL:   "crit",
	ERROR:   "err",
	WARNING: "warning",
	INFO:    "notice",
	DEBUG:   "debug",
}

// systemLog is one way of reaching the system log.
type systemLog interface {
	write(level int, line string) error
}

// destinations are the ways in, in order of preference: a line goes to the
// first one that takes it. An empty list, or a line that none of them takes,
// leaves standard error.
var destinations []systemLog

// openLog decides how the run will reach the system log: the library first,
// logger(1) second. On Ubuntu the lines show up in journalctl, and in
// /var/log/syslog where rsyslog is installed; on FreeBSD in /var/log/messages.
//
// The facility is daemon, which is where a service of this kind belongs and
// which both systems write to those places.
func openLog() {
	if writer, err := syslog.New(syslog.LOG_DAEMON, programName); err == nil {
		destinations = append(destinations, syslogLibrary{writer})
	}
	if _, err := exec.LookPath("logger"); err == nil {
		destinations = append(destinations, loggerProgram{})
	}
	if len(destinations) == 0 {
		logf(WARNING, "neither the log socket nor logger(1) can be reached:"+
			" writing on standard error, which cron pipes into the log")
	}
}

// logf writes one line, if the level deserves it: the time, the level, the
// text. The time is ISO 8601 with the zone, the same on every line wherever it
// lands; syslog stamps its own too, which is the price of a stamp on the
// terminal and in whatever file a pipe may end up in.
func logf(level int, format string, args ...interface{}) {
	if level > logLevel {
		return
	}
	line := time.Now().Format(time.RFC3339) + " " + levelPrefix[level] + " " + fmt.Sprintf(format, args...)
	for _, destination := range destinations {
		if destination.write(level, line) == nil {
			return
		}
	}
	fmt.Fprintln(os.Stderr, line)
}

// syslogLibrary is the log socket, opened once and held open for the run.
type syslogLibrary struct{ writer *syslog.Writer }

// write hands the line over under the severity severityName gives the level.
func (s syslogLibrary) write(level int, line string) error {
	switch level {
	case FATAL:
		return s.writer.Crit(line)
	case ERROR:
		return s.writer.Err(line)
	case WARNING:
		return s.writer.Warning(line)
	case INFO:
		return s.writer.Notice(line)
	}
	return s.writer.Debug(line)
}

// loggerProgram is logger(1), which reaches the log the way the rest of the
// system does. It costs a process per line, which is why it comes second: it
// is there for the machine where the library cannot open the socket, such as a
// container without /dev/log or a host that only logs over the network.
type loggerProgram struct{}

func (loggerProgram) write(level int, line string) error {
	return runProgram(strings.NewReader(line),
		"logger", "-t", programName, "-p", "daemon."+severityName[level])
}

// fatalf reports what makes going on impossible, and ends the program.
func fatalf(format string, args ...interface{}) {
	logf(FATAL, format, args...)
	os.Exit(1)
}
