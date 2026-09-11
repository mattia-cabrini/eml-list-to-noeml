// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strings"
	"testing"
)

func TestReadConfigurationFiles(t *testing.T) {
	service, err := readServiceConfig("testdata/service.conf")
	if err != nil {
		t.Fatal(err)
	}
	systemHost, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	wantService := ServiceConfig{
		Include:    "testdata/r*.conf",
		OutputDir:  "/var/spool/noeml",
		WorkDir:    "/var/lib/eml-list-to-noeml",
		OwnerUser:  "root",
		OwnerGroup: "mail",
		Hostname:   systemHost,      // the file names none: the machine's own
		LogLevel:   defaultLogLevel, // the file names none either
	}
	if service != wantService {
		t.Errorf("service %+v, want %+v", service, wantService)
	}

	files, err := mailboxConfigFiles(service)
	if err != nil || len(files) != 1 || files[0] != "testdata/root.conf" {
		t.Fatalf("mailbox configuration files %v, %v; want testdata/root.conf only", files, err)
	}
	mailbox, err := readMailboxConfig(files[0])
	if err != nil {
		t.Fatal(err)
	}
	wantMailbox := MailboxConfig{
		Name:              "root",
		File:              "testdata/sample.mbox",
		Recipient:         "admin@example.com",
		GPGKey:            "admin@example.com",
		GPGPassphraseFile: "testdata/root.passphrase",
		// No work_dir in the file: the run fills in the service one.
	}
	if mailbox != wantMailbox {
		t.Errorf("mailbox %+v, want %+v", mailbox, wantMailbox)
	}
}

func TestReadConfigFileReportsWhatIsWrong(t *testing.T) {
	_, err := readMailboxConfig("testdata/incomplete.conf")
	if err == nil || !strings.Contains(err.Error(), "missing recipient, gpg_passphrase_file") {
		t.Errorf("missing keys should all be reported, got %v", err)
	}
	_, err = readConfigFile("testdata/broken.conf")
	if err == nil || !strings.Contains(err.Error(), "broken.conf:3:") {
		t.Errorf("a line without = should be reported with its number, got %v", err)
	}
}

func TestIncludeDir(t *testing.T) {
	if dir, plain := includeDir("/etc/x/conf.d/*.conf"); dir != "/etc/x/conf.d" || !plain {
		t.Errorf("got %q, %v; want the directory, plain", dir, plain)
	}
	if _, plain := includeDir("/etc/x/*/*.conf"); plain {
		t.Error("a wildcard in the directory part is not plain")
	}
}
