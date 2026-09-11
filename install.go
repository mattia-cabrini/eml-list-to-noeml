// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// The `install` command: install or update the service.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// serviceTemplate takes include, output_dir, work_dir, owner_user,
// owner_group, hostname and log_level.
const serviceTemplate = `# eml-list-to-noeml service configuration, written by "eml-list-to-noeml install".
# Glob matching the mailbox configuration files; "eml-list-to-noeml config" adds them.
include = %s
# Where the .noeml envelopes are deposited: the input queue of SimpleQueueMailing.
output_dir = %s
# Watermarks, and the envelopes while they are built.
work_dir = %s
# The deposited envelopes belong to this user and this group, and are read only
# for them and unreadable for everybody else: an envelope is signed, not
# encrypted, so it carries the original message in clear. Leave a name empty to
# leave that one as it is.
owner_user = %s
owner_group = %s
# The name this machine goes by in the envelopes; empty means the system one.
hostname = %s
# How much reaches the log: 0 FATAL, 1 ERROR, 2 WARNING, 3 INFO, 4 DEBUG.
log_level = %s
`

// cronTemplate takes the program, the service configuration and the log tag.
const cronTemplate = `# eml-list-to-noeml: every minute, convert the new messages into .noeml envelopes.
# Written by "eml-list-to-noeml install"; the next one overwrites it.
SHELL=/bin/sh
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
# The program writes into the system log itself; the pipe catches what it cannot
# put there on its own, such as a panic of the Go runtime, so that a line lands
# in the log whatever happens. MAILTO is empty so that cron never mails anything
# either: it would deliver to root's mailbox, which is most likely one of the
# files being converted.
MAILTO=""
* * * * * root %s run %s 2>&1 | logger -t %s -p daemon.err
`

// Who the deposited envelopes belong to, unless the configuration says
// otherwise. SimpleQueueMailing runs as root and reads them as root; the group
// is there to say what they are, and is the one the other logs leaving this
// machine belong to.
const (
	defaultOwnerUser  = "root"
	defaultOwnerGroup = "external-log"
)

// logger is not needed as long as the log socket answers, but it is what the
// run falls back on and what the cron job pipes its standard error into, so a
// system without it has no second way of reaching the log.
var programsNeededAtRunTime = []string{"cmc-eml", "gpg", "logger"}

// install asks for the service configuration, proposing the current values
// when the service is already installed, copies the program, writes the
// configuration and schedules a run every minute through cron.
func install() error {
	current := valuesToPropose(installed.serviceConfig)

	include := ask("Glob of the mailbox configuration files",
		valueOr(current, "include", installed.defaultInclude))
	if dir, plain := includeDir(include); plain {
		offerToCreate(dir, 0o755)
	}
	// Everything here runs as root, SimpleQueueMailing included, so nobody
	// else has any business walking these directories.
	outputDir := askDirectory("Directory where the .noeml envelopes are deposited",
		valueOr(current, "output_dir", installed.defaultOutputDir), 0o700)
	workDir := askDirectory("Working directory (watermarks, envelopes being built)",
		valueOr(current, "work_dir", installed.defaultWorkDir), 0o700)
	ownerUser := ask("User the deposited envelopes belong to",
		valueOr(current, "owner_user", defaultOwnerUser))
	ownerGroup := ask("Group the deposited envelopes belong to",
		valueOr(current, "owner_group", defaultOwnerGroup))
	systemHost, _ := os.Hostname() // only the proposal: see ServiceConfig.Hostname
	hostname := ask("Name this machine goes by in the envelopes",
		valueOr(current, "hostname", systemHost))
	logLevel := ask("Log level (0 FATAL, 1 ERROR, 2 WARNING, 3 INFO, 4 DEBUG)",
		valueOr(current, "log_level", strconv.Itoa(defaultLogLevel)))

	if err := installProgram(); err != nil {
		return err
	}
	configuration := fmt.Sprintf(serviceTemplate,
		include, outputDir, workDir, ownerUser, ownerGroup, hostname, logLevel)
	if err := installFile(installed.serviceConfig, []byte(configuration), 0o644); err != nil {
		return err
	}
	if err := writeCronJob(); err != nil {
		return err
	}

	if previous := current["work_dir"]; previous != "" && previous != workDir {
		fmt.Printf("Warning: the working directory was %s: move its *.watermark files to %s,"+
			" or every message is deposited again.\n", previous, workDir)
	}
	warnAboutMissingPrograms()
	checkOwner(ownerUser, ownerGroup)
	fmt.Println("\nDone. Add a mailbox with `make config`; the cron job already runs every minute.")
	return nil
}

// installProgram copies this very executable into place. The rename over the
// old copy works even while the old copy is running.
func installProgram() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	return installFile(installed.binary, data, 0o755)
}

// writeCronJob schedules the run every minute. The atomic write also changes
// the directory: cron looks for changes there, and an overwrite in place would
// not produce one.
func writeCronJob() error {
	content := fmt.Sprintf(cronTemplate, installed.binary, installed.serviceConfig, programName)
	return installFile(installed.cronFile, []byte(content), 0o644)
}

func warnAboutMissingPrograms() {
	for _, program := range programsNeededAtRunTime {
		if _, err := exec.LookPath(program); err != nil {
			fmt.Printf("Warning: %s is not in PATH; the service needs it at run time.\n", program)
		}
	}
}

// checkOwner says right away whether the names given for the envelopes exist,
// which the service would otherwise only discover at the first run. It is the
// very lookup the run does, so the two cannot disagree on what a name may be.
func checkOwner(ownerUser, ownerGroup string) {
	if _, err := lookupOwner(ownerUser, ownerGroup); err != nil {
		fmt.Printf("Warning: %v; the service needs it at run time.\n", err)
	}
}
