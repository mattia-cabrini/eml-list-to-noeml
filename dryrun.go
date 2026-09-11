// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// The `dry-run` command: convert the whole of one mailbox, watermark or not,
// into plain envelopes deposited where it is told. A way to see what the
// service would make of a mailbox, on any machine, with nothing installed but
// cmc-eml.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// dryRunRecipient is who the plain envelopes are addressed to. The dry run
// reads no configuration, so the address is a placeholder by design: nothing
// it builds is meant to be sent.
const dryRunRecipient = "someone@example.com"

// dryRun turns every message of the mailbox at mailboxPath into a plain
// envelope, unsigned, and deposits them into depositDir, mode 0440, owned by
// whoever runs it. It needs no configuration and touches nothing of the
// service: no watermark is read or written, no run lock is taken, and the
// envelopes are built in a directory of their own inside depositDir, which
// goes away at the end. The log stays on the terminal, since nobody is
// watching syslog for a command typed by hand.
func dryRun(mailboxPath, depositDir string) error {
	if _, err := os.Stat(mailboxPath); err != nil {
		return err
	}
	if info, err := os.Stat(depositDir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory to deposit into", depositDir)
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}

	// Building inside the deposit directory keeps the whole dry run in the one
	// place the operator named, and the sweep of leftovers away from anything
	// else that lives there.
	buildDir := filepath.Join(depositDir, "."+programName+".build")
	if err := os.MkdirAll(buildDir, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(buildDir)

	mailbox := MailboxConfig{
		Name:      "dry-run",
		File:      mailboxPath,
		Recipient: dryRunRecipient,
		WorkDir:   buildDir,
		Hostname:  host,
	}
	nobody := owner{uid: -1, gid: -1} // no name given: the envelopes stay whoever runs this
	logf(INFO, "%s: converting the whole of %s into %s, unsigned, to %s",
		mailbox.Name, mailboxPath, depositDir, dryRunRecipient)

	// Phases 2 and 4 of a run, with the plain envelope in place of the unsigned
	// one and the signing pass, and no watermark on either side.
	written, read := writeOutMessages(mailbox, Watermark{}, (*Message).WritePlain)
	deposited := depositEnvelopes(depositDir, mailbox, nobody)
	if !(read && deposited) {
		return errors.New("the dry run did not go through: see the lines above")
	}
	logf(INFO, "%s: %d message(s) converted", mailbox.Name, len(written))
	return nil
}
