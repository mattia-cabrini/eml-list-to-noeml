// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// Turning a message into a signed .noeml envelope.
//
// An envelope is an e-mail (headers, blank line, body) without a From header,
// which SimpleQueueMailing adds when sending it. It is made in two passes,
// which mirror the signed example shipped with cmc-eml:
//
//  1. WriteUnsigned has cmc-eml build the unsigned envelope, a multipart/mixed
//     entity holding the notice as text body and the original message as
//     attachment. It is the only pass that needs the message in memory, and it
//     runs while the mailbox is being read;
//  2. Sign has gpg sign the unsigned envelope and cmc-eml seal it, with its
//     signature, into the multipart/signed entity that carries the envelope
//     headers. It works on files alone, so it runs once the mailbox has been
//     read and given back.
//
// cmc-eml calls the unsigned envelope "the clear message" (print-clear-eml,
// clear-message): that is why the file it writes is a .clear.eml and the
// signature of it a .clear.asc. Here it is the unsigned envelope throughout.

package main

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// attachmentMIMEType is the content type of the attached message.
// message/rfc822 lets mail clients show it as a message. Strictly speaking
// RFC 2046 forbids base64 for it, which is how cmc-eml has to encode it for the
// signature to survive transport, but the common clients cope. Switch to
// application/octet-stream to be strictly compliant.
const attachmentMIMEType = "message/rfc822"

// Transfer encodings, numbered as cmc-eml does (`fmt`).
const (
	base64Encoding   = "1"
	sevenBitEncoding = "2"
)

// Longer subjects are cut: cmc-eml reads each command into a 4 KiB buffer, and
// 200 characters, even all of them four bytes of UTF-8 and then RFC 2047
// encoded and folded, stay under 2 KiB of it. It is also more than a subject
// line is for.
const maxSubjectLength = 200

// bodyTemplate takes user, host, date and time. The signature separator is a
// bare "--": the notice travels as 7bit text inside the signed part, where
// RFC 3156 forbids trailing white space, which mail servers may strip.
const bodyTemplate = "New message to %s@%s, received on %s at %s.\r\n" +
	"\r\n" +
	"The message is attached.\r\n" +
	"\r\n" +
	"--\r\n" +
	"noreply\r\n"

// An envelope takes three names in the working directory before it is
// deposited:
//
//	<envelope>.noeml.part.unsigned   the unsigned envelope, what gpg signs
//	<envelope>.noeml.part            the signed envelope, while it is written
//	<envelope>.noeml                 the envelope, ready to be deposited
//
// Only the last of the three ends in .noeml, so only the last can be picked up
// by the deposit, and each name is taken with a rename, which happens all at
// once: a pass that dies half way leaves nothing that carries the name of a
// finished thing. Nothing else that lives in the working directory, the
// watermarks, the run lock, what the deposit set aside as .noeml.deposited,
// ends in .noeml or in one of the names filesOf gives.
const (
	envelopeSuffix = ".noeml"
	unsignedSuffix = partialSuffix + ".unsigned"
)

// buildFiles are the files the making of one envelope works with: the names
// above, and the pieces that cmc-eml and gpg hand one another. They are all
// named after the envelope, which is named after the message, so that two
// messages can never write over each other's work; and they are removed as
// soon as they are spent, so that the working directory holds the envelopes
// and nothing else.
type buildFiles struct {
	envelope  string // the finished envelope, the name the deposit looks for
	unsigned  string // the unsigned envelope: the first pass writes it, the second signs it
	partial   string // the envelope while it is being written
	message   string // the original message, which cmc-eml attaches from a file
	body      string // the notice, which cmc-eml sets as the body from a file
	clear     string // where cmc-eml writes the unsigned envelope, before it is renamed
	signature string // the detached signature of the unsigned envelope
}

// filesOf names them all, given the name of the envelope.
func filesOf(envelope string) buildFiles {
	base := strings.TrimSuffix(envelope, envelopeSuffix)
	return buildFiles{
		envelope:  envelope,
		unsigned:  envelope + unsignedSuffix,
		partial:   envelope + partialSuffix,
		message:   base + ".eml",
		body:      base + ".body.txt",
		clear:     base + ".clear.eml",
		signature: base + ".clear.asc",
	}
}

// sweepLeftovers removes what the making of an envelope leaves behind when it
// does not run to the end: an unsigned envelope never signed, a half written
// thing, the pieces an envelope is made out of. None of them is of any use once
// the pass is over, since the watermark does not cover a message whose envelope
// was not built, and the next run makes them again under the same names. The
// finished envelopes are not touched, nor is anything else in the directory.
func sweepLeftovers(workDir string) {
	// A glob star is a fine envelope name to filesOf, and gives the pieces of
	// every envelope as patterns: the sweep follows the naming by construction.
	every := filesOf("*" + envelopeSuffix)
	for _, pattern := range []string{every.unsigned, every.partial,
		every.message, every.body, every.clear, every.signature} {
		leftovers, err := filepath.Glob(filepath.Join(workDir, pattern))
		if err != nil {
			continue // only a work_dir with glob characters in it gets here, and the deposit reports that
		}
		for _, path := range leftovers {
			logf(DEBUG, "removing what was left of an envelope: %s", path)
			os.Remove(path)
		}
	}
}

// WriteUnsigned is the first pass: it builds the unsigned envelope, the
// multipart/mixed of the notice and the message itself that gpg is to sign,
// and leaves it at path with .part.unsigned after it.
//
// It is the only pass that needs the message in memory. The bytes are written
// out, handed to cmc-eml, and let go before it returns, so that reading a
// mailbox of thousands costs the memory of one message.
func (m *Message) WriteUnsigned(path string, mailbox MailboxConfig) error {
	n := m.notice(mailbox)
	file := filesOf(path)
	defer removeFiles(file.message, file.body, file.clear)

	if err := os.WriteFile(file.message, m.Content, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(file.body, []byte(n.body()), 0o600); err != nil {
		return err
	}
	err := cmcEML(
		command("set-body", "path", file.body, "mime-type", "text/plain", "fmt", sevenBitEncoding),
		command("add-attachment", "path", file.message, "filename", n.attachmentName(),
			"mime-type", attachmentMIMEType, "fmt", base64Encoding),
		command("print-clear-eml", "path", file.clear),
	)
	if err != nil {
		return err
	}
	m.Content = nil // it is on disk now, inside the unsigned envelope
	return os.Rename(file.clear, file.unsigned)
}

// Sign is the second pass: it signs the unsigned envelope left by the first
// and seals it, with its signature, into the envelope at path, which is the
// name the deposit looks for. It needs nothing of the message but its headers.
func (m *Message) Sign(path string, mailbox MailboxConfig) error {
	n := m.notice(mailbox)
	file := filesOf(path)
	defer removeFiles(file.signature, file.partial)

	if err := gpgSign(file, mailbox); err != nil {
		return err
	}
	if err := seal(file, n, mailbox.Recipient); err != nil {
		return err
	}
	if err := os.Rename(file.partial, file.envelope); err != nil { // same directory: at once
		return err
	}
	removeFiles(file.unsigned) // the envelope is built: the unsigned one is spent
	return nil
}

// notice is what the recipient of this message will read. Both passes need it.
func (m *Message) notice(mailbox MailboxConfig) notice {
	return notice{user: filepath.Base(mailbox.File), host: mailbox.Hostname, message: m}
}

// notice is what the recipient reads: whose mailbox got a message, and when.
type notice struct {
	user    string // owner of the mailbox
	host    string // this machine
	message *Message
}

func (n notice) body() string {
	received := n.message.Key.Received
	return fmt.Sprintf(bodyTemplate, n.user, n.host,
		received.Format("2006-01-02"), received.Format("15:04:05"))
}

func (n notice) subject() string {
	subject := fmt.Sprintf("New message to %s@%s", n.user, n.host)
	if n.message.Subject != "" {
		subject += ": " + truncate(n.message.Subject, maxSubjectLength)
	}
	return subject
}

func (n notice) attachmentName() string {
	return fmt.Sprintf("%s-%d.eml", n.user, n.message.Key.Received.Unix())
}

// gpgSign writes the detached, ASCII armored OpenPGP signature of the unsigned
// envelope.
//
// cmc-eml declares micalg=pgp-sha256, hence the digest. The signature gets CRLF
// line endings because cmc-eml copies it verbatim into the envelope, where
// every line has to end that way.
func gpgSign(file buildFiles, mailbox MailboxConfig) error {
	err := runProgram(nil, "gpg", "--batch", "--yes", "--quiet",
		"--pinentry-mode", "loopback", "--passphrase-file", mailbox.GPGPassphraseFile,
		"--local-user", mailbox.GPGKey, "--digest-algo", "SHA256",
		"--armor", "--detach-sign", "--output", file.signature, file.unsigned)
	if err != nil {
		return err
	}

	armored, err := os.ReadFile(file.signature)
	if err != nil {
		return err
	}
	lines := bytes.ReplaceAll(armored, []byte("\r\n"), []byte("\n"))
	return os.WriteFile(file.signature, bytes.ReplaceAll(lines, []byte("\n"), []byte("\r\n")), 0o600)
}

// seal has cmc-eml wrap the unsigned envelope and its signature into the
// multipart/signed envelope, under the envelope headers. It writes the partial
// name: the envelope takes its own with the rename that follows.
func seal(file buildFiles, n notice, recipient string) error {
	return cmcEML(
		header("To", recipient),
		textHeader("Subject", n.subject()),
		header("Date", time.Now().Format(time.RFC1123Z)),
		header("Message-ID", fmt.Sprintf("<%s@%s>", n.message.Key.SHA1, n.host)),
		header("Auto-Submitted", "auto-generated"),
		command("print-signed-eml",
			"clear-message", file.unsigned, "signature", file.signature, "path", file.partial),
	)
}

// header is the cmc-eml command that adds a header whose value is written as
// it is: addresses, dates, identifiers.
func header(name, value string) string {
	return command("add-header", "key", name, "value", value)
}

// textHeader is the cmc-eml command that adds a free text header. A value
// that is not ASCII is RFC 2047 encoded: mime splits it into encoded words of
// at most 75 characters joined by a space, which are folded here (CRLF and a
// space) so that no header line exceeds the 998 octets allowed by RFC 5322.
// cmc-eml copies the value verbatim, folds included; quoted escapes the CRLF.
func textHeader(name, value string) string {
	value = mime.BEncoding.Encode("utf-8", value)
	value = strings.ReplaceAll(value, "?= =?", "?=\r\n =?")
	return header(name, value)
}

// command is a cmc-eml command line: do=name followed by key="value" pairs.
func command(name string, keyValues ...string) string {
	line := "do=" + name
	for i := 0; i+1 < len(keyValues); i += 2 {
		line += " " + keyValues[i] + "=" + quoted(keyValues[i+1])
	}
	return line
}

// quoted quotes a value for the cmc-eml command language.
func quoted(value string) string {
	escape := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", `\r`, "\n", `\n`)
	return `"` + escape.Replace(value) + `"`
}

// cmcEML feeds the commands to cmc-eml, one per line, then tells it to quit.
func cmcEML(commands ...string) error {
	script := strings.Join(commands, "\n") + "\ndo=quit\n"
	return runProgram(strings.NewReader(script), "cmc-eml")
}

// runProgram runs a program with stdin as its standard input; a failure
// becomes an error carrying what the program said on stderr.
func runProgram(stdin io.Reader, name string, args ...string) error {
	program := exec.Command(name, args...)
	program.Stdin = stdin
	var stderr bytes.Buffer
	program.Stderr = &stderr
	if err := program.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func removeFiles(paths ...string) {
	for _, path := range paths {
		os.Remove(path)
	}
}
