// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// One message of a mailbox: what it is made of, how it is named, how the log
// talks about it.

package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// maxSenderInName is how much of the sender the envelope name may carry. The
// rest is fixed: 10 digits of delivery time, 40 of digest, two separators and
// at most .noeml.part.unsigned after it, 136 bytes in all, well under the 255
// a file name may have on every file system in use.
const maxSenderInName = 64

// MessageKey identifies a message: when the mail delivery agent wrote it into
// the mailbox, and what it contains. The delivery time orders the messages;
// the digest tells apart the ones delivered in the same second, whose time is
// equal. Identifying them by content, and not by the position they have in the
// file, is what keeps a message removed by somebody else from shifting the
// identity of the messages that follow it.
type MessageKey struct {
	Received time.Time
	SHA1     string
}

// Message is one message of a mailbox: its bytes, the "From " separator line
// excluded, and what is needed to name it and to talk about it in the log.
type Message struct {
	Key     MessageKey
	From    string
	To      string
	Subject string

	// Content is the message as the reader handed it over. WriteUnsigned
	// writes it out and sets this to nil, so that reading a mailbox holds one
	// message at a time and never the whole of it.
	Content []byte

	// SuspiciousLines are the lines of this message that begin like a
	// separator line and carry no delivery time, each with the offset it sits
	// at. Such a line is a mistake of the delivery agent away from being a real
	// one, in which case this message is swallowing the next. They travel here
	// rather than being written to the log as they are read, so that they are
	// said of a message that is being dealt with, once, and not at every
	// reading of a mailbox that was dealt with long ago.
	SuspiciousLines []string
}

// delivery is what an mbox separator line says: when the mail delivery agent
// wrote the message into the mailbox, and who handed it over.
type delivery struct {
	received time.Time
	sender   string
}

// beginsLikeSeparator reports whether a line starts the way a separator line
// does; only parseDelivery can say whether it is one.
func beginsLikeSeparator(line []byte) bool {
	return bytes.HasPrefix(line, []byte("From "))
}

// parseDelivery reads a separator line, "From root@host  Fri Sep  5 03:00:00
// 2026", and reports false when the line does not start a message.
//
// Delivery agents escape such a line inside a body as ">From ", but a mailbox
// that a script or a restored spool appended to may well carry an unescaped
// one. Asking for a sender and a delivery time that parses, and not for the
// "From " alone, keeps a line of prose from cutting a message in two, which
// would deposit half of it and leave the other half an unreadable message
// blocking the mailbox for good.
func parseDelivery(line []byte) (delivery, bool) {
	if !beginsLikeSeparator(line) {
		return delivery{}, false
	}
	words := strings.Fields(string(line))
	if len(words) < 7 { // "From", the sender, and the five words of the time
		return delivery{}, false
	}
	received, err := time.ParseInLocation(deliveryTimeLayout,
		strings.Join(words[len(words)-5:], " "), time.Local)
	if err != nil {
		return delivery{}, false
	}
	return delivery{received: received, sender: words[1]}, true
}

// deliveryTimeLayout is the time an mbox separator line ends with.
const deliveryTimeLayout = "Mon Jan 2 15:04:05 2006"

// newMessage builds a message out of what its separator line said and the
// lines that followed it. The content is the message's own from here on: the
// reader has let go of it.
func newMessage(delivery delivery, content []byte) *Message {
	content = withoutTrailingBlankLine(content)

	// The digest covers the whole message, headers and body: two messages
	// differing in a single header, a Message-ID or a Date, are two messages.
	// What it leaves out is mbox packaging: the separator line, whose text
	// depends on the delivery agent and whose delivery time already names the
	// envelope, and the one blank line that closes the message in the file. A
	// message the file leaves without a final newline is taken as having one,
	// so that the last message of a mailbox keeps its digest once the next
	// delivery is appended after it.
	digest := sha1.Sum(content)

	message := &Message{
		Key:     MessageKey{Received: delivery.received, SHA1: hex.EncodeToString(digest[:])},
		Content: content,
	}
	message.readHeaders()
	if message.From == "" {
		message.From = delivery.sender
	}
	return message
}

// readHeaders takes From, To and Subject from the message, as far as it can:
// a message whose headers cannot be parsed is deposited all the same, only
// with less to say about it.
func (m *Message) readHeaders() {
	parsed, err := mail.ReadMessage(bytes.NewReader(m.Content))
	if err != nil {
		return
	}
	m.From = decodedHeader(parsed.Header.Get("From"))
	m.To = decodedHeader(parsed.Header.Get("To"))
	m.Subject = decodedHeader(parsed.Header.Get("Subject"))
}

// decodedHeader is a header value with its RFC 2047 encoded words decoded and
// its folds undone, so that it fits on the one line the log and the envelope
// subject give it.
func decodedHeader(value string) string {
	if decoded, err := new(mime.WordDecoder).DecodeHeader(value); err == nil {
		value = decoded
	}
	return strings.Join(strings.Fields(value), " ")
}

// EnvelopeName is the name of the envelope of this message, and it is the same
// at every run: <delivery time>_<sender>_<digest>.noeml. Building the same
// message twice therefore writes the same file twice, instead of depositing
// the message twice.
func (m *Message) EnvelopeName() string {
	sender := fileNameSafe(addressOf(m.From), maxSenderInName)
	return fmt.Sprintf("%d_%s_%s", m.Key.Received.Unix(), sender, m.Key.SHA1) + envelopeSuffix
}

// String is how the log names a message: enough to find it in the mailbox.
func (m *Message) String() string {
	return fmt.Sprintf("from %q to %q of %s (%s)",
		m.From, m.To, m.Key.Received.Format(time.RFC3339), m.Key.SHA1)
}

// withoutTrailingBlankLine drops the empty line that stands between a message
// and the next one in the mailbox, which belongs to neither, and makes sure
// that the message ends with a newline (the last one of a file may not).
func withoutTrailingBlankLine(content []byte) []byte {
	if bytes.HasSuffix(content, []byte("\n\n")) {
		return content[:len(content)-1]
	}
	if !bytes.HasSuffix(content, []byte("\n")) {
		return append(content, '\n')
	}
	return content
}

// addressOf is the bare address of a From header, "Cron Daemon <root@host>"
// included; a header that is not an address is returned as it is.
func addressOf(from string) string {
	if address, err := mail.ParseAddress(from); err == nil {
		return address.Address
	}
	return from
}

// fileNameSafe keeps of s the first max characters, with every one that could
// be read as more than itself replaced: the sender comes from a header written
// by a stranger, and the name goes into paths, globs and cmc-eml commands.
func fileNameSafe(s string, max int) string {
	if safe := strings.Map(fileNameCharacter, truncate(s, max)); safe != "" {
		return safe
	}
	return "unknown"
}

// fileNameCharacter keeps letters, digits and the few characters an address is
// made of, and turns everything else into an underscore.
func fileNameCharacter(character rune) rune {
	switch {
	case character >= 'a' && character <= 'z',
		character >= 'A' && character <= 'Z',
		character >= '0' && character <= '9',
		strings.ContainsRune(".@+-", character):
		return character
	}
	return '_'
}

// truncate cuts s to at most n characters, not bytes, so that a multi byte
// character is never cut in half.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
