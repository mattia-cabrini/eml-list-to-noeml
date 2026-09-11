// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleMailbox holds two messages delivered in the same second; see the
// comment at its top.
const sampleMailbox = "testdata/sample.mbox"

// mailboxToRead is a copy of the sample mailbox in a scratch directory.
// Reading a mailbox creates a lock file next to it, so the copy keeps
// testdata/, which git tracks, untouched.
func mailboxToRead(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(sampleMailbox)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scratchDir(t), "sample.mbox")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readMailbox is every message the reader hands out of path, watermark in force.
func readMailbox(t *testing.T, path string, watermark Watermark) []*Message {
	t.Helper()
	reader, err := newMailboxReader(path, watermark)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var messages []*Message
	for {
		message, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		if message == nil {
			return messages
		}
		messages = append(messages, message)
	}
}

func TestReadMailbox(t *testing.T) {
	messages := readMailbox(t, mailboxToRead(t), Watermark{})
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}

	for i, message := range messages {
		if !message.Key.Received.Equal(sampleDelivery) {
			t.Errorf("message %d received %v, want %v", i, message.Key.Received, sampleDelivery)
		}
		if len(message.Key.SHA1) != 40 {
			t.Errorf("message %d digest %q, want 40 hexadecimal characters", i, message.Key.SHA1)
		}
	}
	if messages[0].Key.SHA1 == messages[1].Key.SHA1 {
		t.Error("two different messages, one digest")
	}

	first, second := messages[0], messages[1]
	if first.Subject != "Ciao è" || first.From != "Cron Daemon <root@host>" || first.To != "root@host" {
		t.Errorf("first headers: from %q to %q subject %q", first.From, first.To, first.Subject)
	}
	if got := string(first.Content); got != "From: Cron Daemon <root@host>\nTo: root@host\n"+
		"Subject: =?utf-8?q?Ciao_=C3=A8?=\n\nBody one.\n" {
		t.Errorf("first content %q: the separator line and the blank line after it should be gone", got)
	}
	// The two lines that begin like a separator line belong to the body: the
	// escaped one and, above all, the unescaped one, which must not cut the
	// message in two.
	if got := string(second.Content); got != "From: root@host\nTo: root@host\nSubject: Second\n\n"+
		">From the body\nFrom the desk of Bob\nBody two.\n" {
		t.Errorf("second content %q: should hold both From lines and end with one newline", got)
	}
	// The unescaped one is reported with the message it belongs to, not with
	// the reading of the mailbox.
	if len(first.SuspiciousLines) != 0 {
		t.Errorf("first message: %v, want nothing suspicious in it", first.SuspiciousLines)
	}
	if len(second.SuspiciousLines) != 1 ||
		!strings.Contains(second.SuspiciousLines[0], `"From the desk of Bob"`) {
		t.Errorf("second message: %v, want the unescaped From line of its body", second.SuspiciousLines)
	}
}

// The watermark must exclude a message by what it contains, so that removing
// another message from the mailbox cannot carry it over unsent.
func TestReadMailboxSkipsWhatTheWatermarkCovers(t *testing.T) {
	path := mailboxToRead(t)
	all := readMailbox(t, path, Watermark{})

	afterFirst := readMailbox(t, path, Watermark{}.with(all[0].Key))
	if len(afterFirst) != 1 || afterFirst[0].Key.SHA1 != all[1].Key.SHA1 {
		t.Fatalf("after the first message: %d left, want the second one only", len(afterFirst))
	}
	afterBoth := readMailbox(t, path, Watermark{}.with(all[0].Key).with(all[1].Key))
	if len(afterBoth) != 0 {
		t.Fatalf("after both: %d left, want none", len(afterBoth))
	}
}

func TestOpenMailboxThatIsNotThere(t *testing.T) {
	if _, err := newMailboxReader(filepath.Join(scratchDir(t), "missing.mbox"), Watermark{}); err == nil {
		t.Error("expected an error")
	}
}

func TestParseDelivery(t *testing.T) {
	from, opensMessage := parseDelivery([]byte("From root@host  Fri Sep  5 03:00:00 2026\n"))
	if !opensMessage || from.sender != "root@host" || !from.received.Equal(sampleDelivery) {
		t.Errorf("a real separator line gave %+v, %v", from, opensMessage)
	}

	// Only a line that says who delivered the message and when starts one.
	for _, line := range []string{
		"From the desk of Bob\n",
		"From nobody\n",
		"From Fri Sep  5 03:00:00 2026\n", // a time but no sender
		">From root@host  Fri Sep  5 03:00:00 2026\n",
		"From: Cron Daemon <root@host>\n",
		"Body one.\n",
	} {
		if _, opensMessage := parseDelivery([]byte(line)); opensMessage {
			t.Errorf("%q should not start a message", line)
		}
	}
}
