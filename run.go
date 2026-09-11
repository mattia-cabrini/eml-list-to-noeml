// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// The `run` command, in two phases:
//
//  1. read the service configuration, and say in the log what the run will do;
//  2. take the mailbox configurations one at a time.
//
// A mailbox configuration goes in turn through four phases:
//
//  1. read the configuration and the watermark;
//  2. read the mailbox, writing out the unsigned envelope of every message to
//     deposit and letting go of its bytes;
//  3. sign them all, once the mailbox has been given back, each becoming an
//     envelope;
//  4. move every envelope waiting in the working directory into the deposit
//     directory, and update the watermark to the last message enveloped.
//
// The three names an envelope takes on the way are in envelope.go. The order
// of the phases is what keeps the mailbox lock and the memory held for as
// little as possible: the file is read and given back first, the messages are
// let go as they are written out, and gpg is not called until neither is held.

package main

import (
	"errors"
	"os"
	"path/filepath"
)

// run reports whether every mailbox went through. What went wrong is in the
// log already: the caller has only an exit status to set.
func run(serviceConfigPath string) bool {
	openLog()

	// Phase 1: the service configuration.
	service, err := readServiceConfig(serviceConfigPath)
	if err != nil {
		logf(ERROR, "%v", err)
		return false
	}
	logLevel = service.LogLevel

	own, err := lookupOwner(service.OwnerUser, service.OwnerGroup)
	if err != nil {
		logf(ERROR, "%v", err)
		return false
	}
	logf(INFO, "depositing into %s, as %s, mode %#o", service.OutputDir, own, envelopePerm)

	lock, err := lockRun(service.WorkDir)
	switch {
	case errors.Is(err, errBusy):
		logf(WARNING, "the previous run is still going: skipping this one")
		return true
	case err != nil:
		logf(ERROR, "%v", err)
		return false
	}
	defer lock.Close() // closing the file drops the lock

	// Phase 2: the mailbox configurations, one at a time.
	configPaths, err := mailboxConfigFiles(service)
	if err != nil {
		logf(ERROR, "%v", err)
		return false
	}
	if len(configPaths) == 0 {
		logf(WARNING, "no mailbox configuration matches %s", service.Include)
	}
	converted := true
	for _, path := range configPaths {
		if !convertMailbox(service, own, path) {
			converted = false
		}
	}
	return converted
}

// convertMailbox runs the four phases of one mailbox configuration and reports
// whether they went through without errors. Whatever goes wrong here concerns
// this mailbox only: the other configurations are processed anyway.
func convertMailbox(service ServiceConfig, own owner, configPath string) bool {
	// Phase 1: the mailbox configuration and its watermark. Two things come
	// from the service configuration, the host name, which the file never
	// says, and the working directory, when the file names none: the run fills
	// them in here, so that every phase reads them from one place.
	mailbox, err := readMailboxConfig(configPath)
	if err != nil {
		logf(ERROR, "%v", err)
		return false
	}
	mailbox.Hostname = service.Hostname
	if mailbox.WorkDir == "" {
		mailbox.WorkDir = service.WorkDir
	}
	watermark, err := readWatermark(service.WorkDir, mailbox.Name)
	if err != nil {
		logf(ERROR, "%s: %v", mailbox.Name, err)
		return false
	}
	logf(INFO, "%s: converting %s", mailbox.Name, mailbox.File)

	// Phase 2: read the mailbox, writing out the unsigned envelope of each
	// message to deposit. Phase 3: sign them all.
	unsigned, read := writeUnsignedEnvelopes(mailbox, watermark)
	enveloped, signed := signEnvelopes(mailbox, unsigned)

	// Phase 4: deposit, and update the watermark. The deposit runs even when
	// nothing was built, because an envelope a previous run could not move may
	// be waiting.
	deposited := depositEnvelopes(service.OutputDir, mailbox, own)
	marked := updateWatermark(service.WorkDir, mailbox.Name, watermark, enveloped)
	return read && signed && deposited && marked
}

// writeUnsignedEnvelopes is phase 2, the first of the two passes that make an
// envelope: it reads the mailbox and turns every message the watermark does
// not cover into an unsigned envelope on disk, which is what gpg will sign. It
// returns the messages that now have one, with their bytes let go.
//
// A message that cannot be turned into one stops the mailbox: skipping it
// would send the messages behind it and lose it silently, so it is reported
// and the ones before it go on.
func writeUnsignedEnvelopes(mailbox MailboxConfig, watermark Watermark) ([]*Message, bool) {
	reader, ok := openMailbox(mailbox, watermark)
	if reader == nil {
		return nil, ok
	}
	defer reader.Close()

	written := map[string]bool{}
	var unsigned []*Message
	for {
		message, err := reader.Next()
		if err != nil {
			logf(ERROR, "%s: reading %s: %v", mailbox.Name, mailbox.File, err)
			return unsigned, false
		}
		if message == nil {
			return unsigned, true
		}

		// Two messages of the same second that are the same byte for byte,
		// headers included, are one message here: nothing tells them apart,
		// so building the second would only write the first over again. It
		// takes a sender that writes neither Message-ID nor Date.
		name := message.EnvelopeName()
		if written[name] {
			logf(WARNING, "%s: %s of %s is there twice, byte for byte: one envelope carries both",
				mailbox.Name, message, mailbox.File)
			continue
		}
		written[name] = true

		if err := message.WriteUnsigned(filepath.Join(mailbox.WorkDir, name), mailbox); err != nil {
			logf(ERROR, "%s: cannot write the unsigned envelope of %s of %s: %v",
				mailbox.Name, message, mailbox.File, err)
			return unsigned, false
		}
		logf(DEBUG, "%s: wrote %s%s", mailbox.Name, name, unsignedSuffix)
		unsigned = append(unsigned, message)
	}
}

// openMailbox opens the mailbox of a configuration for reading, reporting what
// stands in the way. A nil reader and a true mean that there is simply nothing
// to read this run.
func openMailbox(mailbox MailboxConfig, watermark Watermark) (*MailboxReader, bool) {
	reader, err := newMailboxReader(mailbox.File, watermark)
	switch {
	case errors.Is(err, os.ErrNotExist):
		logf(DEBUG, "%s: there is no %s yet", mailbox.Name, mailbox.File)
		return nil, true
	case errors.Is(err, errBusy):
		logf(WARNING, "%s: %s %v, retrying next run", mailbox.Name, mailbox.File, err)
		return nil, true
	case err != nil:
		logf(ERROR, "%s: %v", mailbox.Name, err)
		return nil, false
	}
	return reader, true
}

// signEnvelopes is phase 3, the second pass: it goes over the unsigned
// envelopes phase 2 left, each becoming the envelope the deposit looks for. It
// needs no lock and no message in memory, only gpg and cmc-eml.
//
// A message that cannot be signed stops the mailbox, for the reason phase 2
// stops: the watermark reaches the last message dealt with, so letting a later
// one past would carry this one over unsent.
func signEnvelopes(mailbox MailboxConfig, unsigned []*Message) ([]*Message, bool) {
	defer sweepLeftovers(mailbox.WorkDir)

	var enveloped []*Message
	for _, message := range unsigned {
		name := message.EnvelopeName()
		if err := message.Sign(filepath.Join(mailbox.WorkDir, name), mailbox); err != nil {
			logf(ERROR, "%s: cannot sign the envelope of %s of %s: %v",
				mailbox.Name, message, mailbox.File, err)
			return enveloped, false
		}
		logf(DEBUG, "%s: signed %s", mailbox.Name, name)
		enveloped = append(enveloped, message)
	}
	return enveloped, true
}

// depositEnvelopes moves into the deposit directory every envelope waiting in
// the working directory: the ones just built and the ones a previous run could
// not move. One that cannot be moved now is not blocking, and is left where it
// is for a later run to take.
func depositEnvelopes(outputDir string, mailbox MailboxConfig, own owner) bool {
	waiting, err := envelopesWaiting(mailbox.WorkDir)
	if err != nil {
		logf(ERROR, "%s: cannot list the envelopes waiting in %s: %v", mailbox.Name, mailbox.WorkDir, err)
		return false
	}

	moved := 0
	for _, path := range waiting {
		if err := moveInto(outputDir, path, own); err != nil {
			logf(WARNING, "%s: could not deposit %s into %s: %v", mailbox.Name, path, outputDir, err)
			continue
		}
		logf(INFO, "%s: deposited %s", mailbox.Name, filepath.Base(path))
		moved++
	}
	if len(waiting) > 0 {
		logf(INFO, "%s: %d envelope(s) of %d deposited", mailbox.Name, moved, len(waiting))
	}
	return true
}

// updateWatermark moves the watermark of a mailbox up to the last message
// enveloped, whether or not its envelope was deposited: a message it covers is
// not built again, its envelope is simply still waiting in the working
// directory. When nothing was enveloped the watermark stands where it is.
func updateWatermark(workDir, name string, watermark Watermark, enveloped []*Message) bool {
	if len(enveloped) == 0 {
		return true
	}
	for _, message := range enveloped {
		watermark = watermark.with(message.Key)
	}
	if err := writeWatermark(workDir, name, watermark); err != nil {
		logf(ERROR, "%s: could not update the watermark: %v", name, err)
		return false
	}
	return true
}

// envelopesWaiting are the envelopes in a working directory, oldest first:
// their name begins with the delivery time of the message they carry, and
// nothing else that lives there ends in .noeml (envelope.go says what does).
func envelopesWaiting(workDir string) ([]string, error) {
	return sortedGlob(filepath.Join(workDir, "*"+envelopeSuffix))
}
