// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// The `config` command: add a mailbox configuration, or update an existing one.

package main

import (
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
)

// mailboxTemplate takes file, recipient, gpg_key, gpg_passphrase_file and work_dir.
const mailboxTemplate = `# eml-list-to-noeml mailbox configuration, written by "eml-list-to-noeml config".
# The mbox file to convert.
file = %s
# Who receives the messages: one or more e-mail addresses, comma separated, no display names.
recipient = %s
# The GPG key that signs them: key id, fingerprint or e-mail of a key in root's keyring.
gpg_key = %s
# File whose first line is the passphrase of that key.
gpg_passphrase_file = %s
# Where the envelopes of this mailbox are built, before being deposited.
work_dir = %s
`

func configureMailbox() error {
	service, err := readServiceConfig(installed.serviceConfig)
	if err != nil {
		return fmt.Errorf("%w (is the service installed? run `make install` first)", err)
	}
	confDir, plain := includeDir(service.Include)
	if !plain {
		return fmt.Errorf("cannot tell where to put the file: include = %s has wildcards in its directory part",
			service.Include)
	}

	name := ask("Name of the configuration (it names its watermark too)", "root")
	// The file has to match the include glob, or the run never sees it: the
	// directory comes from the glob, and so does the extension.
	path := filepath.Join(confDir, name+filepath.Ext(service.Include))
	current := valuesToPropose(path)

	file := ask("Mailbox file to convert", valueOr(current, "file", "/var/mail/"+name))
	recipient := ask("Recipient of the messages (e-mail address)", valueOr(current, "recipient", ""))
	if err := checkRecipients(recipient); err != nil {
		return err
	}
	gpgKey := ask("GPG key that signs the messages (key id, fingerprint or e-mail)",
		valueOr(current, "gpg_key", ""))
	passphraseFile := ask("File holding the passphrase of the key",
		valueOr(current, "gpg_passphrase_file", filepath.Join(installed.etcDir, name+".passphrase")))
	// Only root works in here, and the messages pass through it in clear.
	workDir := askDirectory("Working directory where its envelopes are built",
		valueOr(current, "work_dir", service.WorkDir), 0o700)

	content := fmt.Sprintf(mailboxTemplate, file, recipient, gpgKey, passphraseFile, workDir)
	if err := installFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println("The next run picks it up.")
	warnAboutMissingPassphraseFile(passphraseFile)
	return nil
}

// checkRecipients makes sure that value is a comma separated list of bare
// e-mail addresses: SimpleQueueMailing hands each of them to the SMTP server
// as it is, so display names would not be delivered.
func checkRecipients(value string) error {
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		address, err := mail.ParseAddress(item)
		if err != nil || address.Name != "" || address.Address != item {
			return fmt.Errorf("recipient %q is not a bare e-mail address", item)
		}
	}
	return nil
}

func warnAboutMissingPassphraseFile(path string) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("Warning: %s does not exist. Create it, readable by root only, before the first run:\n", path)
		fmt.Printf("  sh -c 'umask 077; echo \"the passphrase\" > %s'\n", path)
	}
}
