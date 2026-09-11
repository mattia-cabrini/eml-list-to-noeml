// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// Reading a mailbox in mbox format, such as /var/mail/root.
//
// MailboxReader is a state machine over the lines of the file: whatever comes
// before the first separator line is not a message, every separator line opens
// one, and the next separator line or the end of the file closes it. Every
// message of the file is read, so that the ones already dealt with can be
// discarded by what they contain rather than by the position they have.
//
// The file is read through a cache of one fixed size, filled over and over
// rather than grown: what has been read out of it is already in a message, so
// there is nothing left in it worth keeping, and the cache is let go as soon
// as the file is over. What outlives it is the messages, each holding its own
// bytes; the ones the watermark covers are let go here as they are read, and
// the caller writes the others out and lets go of their bytes in turn, so
// that a backlog is never held whole. The read that reaches the end of the
// file gives the mailbox back at once, locks and all, so a mail delivery agent
// waiting to write waits for a read, not for the run.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// cacheSize is how much of the mailbox one read takes in. Nothing depends on
// it: a line longer than this runs on past it (nextLine). 64 MiB reads the
// whole of most mailboxes in one read, and costs nothing on a small one, since
// the pages a read never reaches are never touched.
const cacheSize = 64 << 20 // 64 MiB

// MailboxReader hands out the messages of an mbox file one at a time.
type MailboxReader struct {
	lock      *mailboxLock // nil once the mailbox has been given back
	watermark Watermark

	// The cache: a window on the file, and where that window sits in it.
	cache []byte
	base  int64 // the offset in the file of cache[0]
	next  int   // the index in the cache of the next byte to hand out
	end   int   // how much of the cache the last read filled

	line   []byte // the line being handed out, reused from line to line
	lineAt int64  // the offset in the file where that line begins

	// The message being read: what its separator line said, the lines of it so
	// far, and the ones among them that look like a separator line and are
	// not. Nothing else of it is kept.
	inMessage       bool
	delivery        delivery
	content         []byte
	suspiciousLines []string
}

// newMailboxReader locks the mailbox at path and prepares to read it. It fails
// with errBusy when another program is using the mailbox, and with a not exist
// error when there is no such mailbox.
func newMailboxReader(path string, watermark Watermark) (*MailboxReader, error) {
	lock, err := lockMailbox(path)
	if err != nil {
		return nil, err
	}
	return &MailboxReader{lock: lock, watermark: watermark}, nil
}

// Close gives the mailbox back, if reading it has not already done so.
func (r *MailboxReader) Close() {
	if r.lock != nil {
		r.lock.release()
		r.lock = nil
	}
}

// Next returns the next message to deposit, or nil when the mailbox is over.
// The messages the watermark already covers are read like the others and
// discarded here. The suspicious lines of a message are reported here, when it
// is handed out: Message.SuspiciousLines says why not sooner.
func (r *MailboxReader) Next() (*Message, error) {
	for {
		message, err := r.readMessage()
		if message == nil || err != nil {
			return nil, err
		}
		if r.watermark.covers(message.Key) {
			logf(DEBUG, "already enveloped: %s", message)
			continue
		}
		for _, line := range message.SuspiciousLines {
			logf(WARNING, "%s: no delivery time on it, so it does not start a message"+
				" but belongs to the one %s", line, message)
		}
		return message, nil
	}
}

// readMessage returns the next message of the file, covered or not, and nil
// once the file is over.
func (r *MailboxReader) readMessage() (*Message, error) {
	for {
		line, err := r.nextLine()
		if err != nil {
			return nil, err
		}
		if line == nil {
			return r.closeMessage(), nil // the file is over
		}

		delivery, opensMessage := parseDelivery(line)
		switch {
		case opensMessage:
			// A separator line closes the message before it and opens a new one.
			message := r.closeMessage()
			r.inMessage, r.delivery = true, delivery
			if message != nil {
				return message, nil
			}
		case r.inMessage:
			r.noteIfSuspicious(line)
			r.content = append(r.content, line...)
		case beginsLikeSeparator(line):
			// What precedes the first separator line is not a message. A line
			// that begins like one is worth saying here and now, since there is
			// no message to carry it: a separator line written wrong there
			// leaves the message after it looking like more of the preamble,
			// and that message is never read at all.
			logf(WARNING, "%s: no delivery time on it, and it comes before the first message:"+
				" whatever follows it is not read", r.describe(line))
		}
	}
}

// noteIfSuspicious keeps a line of the message being read that begins like a
// separator line and carries no delivery time, for Next to report with the
// message.
func (r *MailboxReader) noteIfSuspicious(line []byte) {
	if beginsLikeSeparator(line) {
		r.suspiciousLines = append(r.suspiciousLines, r.describe(line))
	}
}

// describe is how the log points at a line: where it is in the file, and what
// it says.
func (r *MailboxReader) describe(line []byte) string {
	return fmt.Sprintf("byte %d, %q", r.lineAt, bytes.TrimRight(line, "\r\n"))
}

// closeMessage hands over the message read so far, and leaves nothing of it in
// the reader. It returns nil when no message is open: before the first
// separator line, and once the last one has been handed out.
func (r *MailboxReader) closeMessage() *Message {
	if !r.inMessage {
		return nil
	}
	message := newMessage(r.delivery, r.content)
	message.SuspiciousLines = r.suspiciousLines
	r.inMessage, r.content, r.suspiciousLines = false, nil, nil
	return message
}

// nextLine returns the next line of the mailbox, its newline included, and nil
// once the file is over.
//
// The line belongs to the reader and is good until the next call: whoever
// needs to keep it copies it, which is what lets the cache be filled again
// instead of growing with the file.
func (r *MailboxReader) nextLine() ([]byte, error) {
	r.line = r.line[:0]
	for {
		if r.next == r.end {
			more, err := r.fill()
			if err != nil {
				return nil, err
			}
			if !more {
				r.cache = nil // nothing will be read into it again
				break
			}
		}
		if len(r.line) == 0 {
			r.lineAt = r.base + int64(r.next)
		}

		window := r.cache[r.next:r.end]
		if end := bytes.IndexByte(window, '\n'); end >= 0 {
			r.next += end + 1
			r.line = append(r.line, window[:end+1]...)
			return r.line, nil
		}
		r.line = append(r.line, window...) // the line goes on past the cache
		r.next = r.end
	}

	if len(r.line) == 0 {
		return nil, nil
	}
	return r.line, nil // a last line the file left without a newline
}

// fill reads the next stretch of the mailbox into the cache, over whatever was
// in it, and reports whether it found anything. Reaching the end of the file
// gives the mailbox back there and then.
func (r *MailboxReader) fill() (bool, error) {
	if r.lock == nil {
		return false, nil // the file is over and given back already
	}
	if r.cache == nil {
		r.cache = make([]byte, cacheSize)
	}
	r.base += int64(r.end)
	r.next, r.end = 0, 0

	read, err := io.ReadFull(r.lock.file, r.cache)
	r.end = read

	switch {
	case err == nil:
		return true, nil // a full cache: the file may well have more
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		r.Close() // the file is over: nothing else needs it
		return read > 0, nil
	}
	return false, err
}
