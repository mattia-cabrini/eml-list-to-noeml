// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// What the install, config and purge commands share: where things go, how to
// ask, how an installed file is written.

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// programName names the executable, the configuration and the syslog entries.
const programName = "eml-list-to-noeml"

// layout is where the installed pieces live.
type layout struct {
	binary        string // the program
	etcDir        string // service and mailbox configurations
	serviceConfig string
	cronFile      string

	// Proposed when the service is installed for the first time.
	defaultInclude   string
	defaultOutputDir string
	defaultWorkDir   string
}

// installed is the layout of this system. Local software lives under
// /usr/local on both systems, but FreeBSD keeps its configuration in
// /usr/local/etc and its state in /var/db.
var installed = systemLayout()

func systemLayout() layout {
	etcDir, cronDir, workDir := "/etc/"+programName, "/etc/cron.d", "/var/lib/"+programName
	if runtime.GOOS == "freebsd" {
		etcDir = "/usr/local/etc/" + programName
		cronDir = "/usr/local/etc/cron.d"
		workDir = "/var/db/" + programName
	}
	return layout{
		binary:           "/usr/local/bin/" + programName,
		etcDir:           etcDir,
		serviceConfig:    etcDir + "/service.conf",
		cronFile:         cronDir + "/" + programName,
		defaultInclude:   etcDir + "/conf.d/*.conf",
		defaultOutputDir: "/var/spool/noeml",
		defaultWorkDir:   workDir,
	}
}

var terminal = bufio.NewReader(os.Stdin)

// ask asks for a value on the terminal; an empty answer picks the fallback.
// When nobody is left to answer (end of input) there is nothing left to do.
func ask(question, fallback string) string {
	for {
		if fallback != "" {
			fmt.Printf("%s [%s]: ", question, fallback)
		} else {
			fmt.Printf("%s: ", question)
		}

		line, err := terminal.ReadString('\n')
		if err != nil && line == "" {
			fatalf("aborted: no answer")
		}
		if answer := strings.TrimSpace(line); answer != "" {
			return answer
		}
		if fallback != "" {
			return fallback
		}
		fmt.Println("A value is required.")
	}
}

// confirm asks a yes/no question; anything but yes is no.
func confirm(question string) bool {
	fmt.Printf("%s [N/y]: ", question)
	line, _ := terminal.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// askDirectory asks for a directory and offers to create it when it is not
// there. Nothing is created without a yes.
func askDirectory(question, fallback string, mode os.FileMode) string {
	path := ask(question, fallback)
	offerToCreate(path, mode)
	return path
}

// offerToCreate offers to create a directory that is not there, and says what
// the answer costs when it is no. It may well return with the directory still
// missing: the directories the service works in are the operator's to create,
// never made behind their back.
func offerToCreate(path string, mode os.FileMode) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	if !confirm(fmt.Sprintf("%s does not exist. Create it?", path)) {
		fmt.Println("  Not created. The service needs it: create it before the first run.")
		return
	}
	if err := os.MkdirAll(path, mode); err != nil {
		fmt.Printf("  Warning: could not create %s: %v\n", path, err)
		return
	}
	fmt.Printf("  Created %s, mode %#o\n", path, mode)
}

// valuesToPropose are the values of the configuration file at path, to be
// proposed as defaults, and says so; a missing or unreadable file has none.
func valuesToPropose(path string) map[string]string {
	file, err := readConfigFile(path)
	if err != nil {
		return nil // reading a nil map gives "", which is what valueOr wants
	}
	if len(file.values) > 0 {
		fmt.Printf("Found %s: its values are proposed as defaults.\n", path)
	}
	return file.values
}

// valueOr is values[key], or fallback when the key is absent or empty.
func valueOr(values map[string]string, key, fallback string) string {
	if value := values[key]; value != "" {
		return value
	}
	return fallback
}

// installFile puts one file of the installation in place, creating its
// directory. Every such file may be in use while the installer overwrites it,
// a run reading its configuration, cron reading its table, the old program
// still running: written atomically, each of them sees the old file whole or
// the new one whole, never half of either.
func installFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeFileAtomically(path, data, perm); err != nil {
		return err
	}
	fmt.Printf("Written %s\n", path)
	return nil
}
