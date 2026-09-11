// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// The two kinds of configuration file. Both are lists of `key = value` lines;
// blank lines and lines starting with # are ignored.
//
// The service configuration, one, named on the command line:
//
//	include = /etc/eml-list-to-noeml/conf.d/*.conf
//	output_dir = /var/spool/noeml
//	work_dir = /var/lib/eml-list-to-noeml
//	owner_user = root
//	owner_group = external-log
//	hostname = mail.example.com
//	log_level = 3
//
// The mailbox configurations, one per file to convert, matched by include:
//
//	file = /var/mail/root
//	recipient = admin@example.com
//	gpg_key = admin@example.com
//	gpg_passphrase_file = /etc/eml-list-to-noeml/root.passphrase
//	work_dir = /var/lib/eml-list-to-noeml

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type ServiceConfig struct {
	Include   string // glob matching the mailbox configuration files
	OutputDir string // where the .noeml envelopes are deposited
	WorkDir   string // watermarks, and the envelopes while they are built

	// The deposited envelopes belong to this user and group. They carry the
	// original messages in clear, so they must not be readable by anybody
	// else; an empty name leaves that ownership as it is.
	OwnerUser  string
	OwnerGroup string

	// Hostname is the name this machine goes by in the envelopes: the notice
	// says which mailbox on which machine got a message. A configuration that
	// names none gets the system host name, which is what the machine calls
	// itself and not always the name the reader of the envelope knows it by.
	Hostname string

	LogLevel int // the least severe level that gets written
}

type MailboxConfig struct {
	Name              string // file name without extension; it names the watermark too
	File              string // the mbox file to convert
	Recipient         string // who receives the envelopes
	GPGKey            string // the key that signs them: key id, fingerprint or e-mail
	GPGPassphraseFile string // its first line is the passphrase of that key

	// WorkDir is where its envelopes are built. A file that names none gets
	// the service one, and Hostname is never in the file at all: the run fills
	// both in from the service configuration before the envelopes are built.
	WorkDir  string
	Hostname string
}

func readServiceConfig(path string) (ServiceConfig, error) {
	file, err := readConfigFile(path)
	if err != nil {
		return ServiceConfig{}, err
	}
	config := ServiceConfig{
		Include:    file.required("include"),
		OutputDir:  file.required("output_dir"),
		WorkDir:    file.required("work_dir"),
		OwnerUser:  file.required("owner_user"),
		OwnerGroup: file.required("owner_group"),
	}
	if err := file.missingKeys(); err != nil {
		return ServiceConfig{}, err
	}

	config.LogLevel, err = strconv.Atoi(file.optional("log_level", strconv.Itoa(defaultLogLevel)))
	if err != nil || config.LogLevel < FATAL || config.LogLevel > DEBUG {
		return ServiceConfig{}, fmt.Errorf("%s: log_level must be a number from %d to %d", path, FATAL, DEBUG)
	}

	// A configuration that names no host name is asking for the system one. It
	// is looked up here, with the configuration, so that a machine that does
	// not know its own name fails the run before any envelope is built.
	config.Hostname = file.optional("hostname", "")
	if config.Hostname == "" {
		if config.Hostname, err = os.Hostname(); err != nil {
			return ServiceConfig{}, fmt.Errorf(
				"%s: no hostname in it, and this machine does not know its own: %w", path, err)
		}
	}
	return config, nil
}

// includeDir is the directory the include glob looks in, and whether it is a
// plain one: a wildcard in the directory part leaves no single place to create
// and to write a mailbox configuration into.
func includeDir(include string) (dir string, plain bool) {
	dir = filepath.Dir(include)
	return dir, !strings.ContainsAny(dir, "*?[")
}

// mailboxConfigFiles are the mailbox configuration files matched by the
// service include, sorted by path.
func mailboxConfigFiles(service ServiceConfig) ([]string, error) {
	paths, err := sortedGlob(service.Include)
	if err != nil {
		return nil, fmt.Errorf("include = %s: %w", service.Include, err)
	}
	return paths, nil
}

// sortedGlob is filepath.Glob with its matches in path order, which Glob does
// not promise and the callers rely on: the configurations are taken in one
// fixed order, the envelopes oldest first.
func sortedGlob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	sort.Strings(matches)
	return matches, err
}

func readMailboxConfig(path string) (MailboxConfig, error) {
	file, err := readConfigFile(path)
	if err != nil {
		return MailboxConfig{}, err
	}
	config := MailboxConfig{
		Name:              configName(path),
		File:              file.required("file"),
		Recipient:         file.required("recipient"),
		GPGKey:            file.required("gpg_key"),
		GPGPassphraseFile: file.required("gpg_passphrase_file"),
		WorkDir:           file.optional("work_dir", ""),
	}
	return config, file.missingKeys()
}

// configName is the name of a mailbox configuration: its file name without extension.
func configName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// configFile is the content of a configuration file plus the required keys
// that were not there, so that one error can report them all.
type configFile struct {
	path    string
	values  map[string]string
	missing []string
}

func readConfigFile(path string) (*configFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	file := &configFile{path: path, values: map[string]string{}}
	for number, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("%s:%d: expected `key = value`", path, number+1)
		}
		file.values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return file, nil
}

// required returns the value of a key the file must have, remembering the key
// when it is missing so that missingKeys can report it.
func (f *configFile) required(key string) string {
	value, found := f.values[key]
	if !found {
		f.missing = append(f.missing, key)
	}
	return value
}

// optional returns the value of a key the file may leave out, or fallback when
// it does.
func (f *configFile) optional(key, fallback string) string {
	if value, found := f.values[key]; found {
		return value
	}
	return fallback
}

// missingKeys reports the required keys that were not there, if any.
func (f *configFile) missingKeys() error {
	if len(f.missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s: missing %s", f.path, strings.Join(f.missing, ", "))
}
