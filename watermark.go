// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// The watermark of a mailbox configuration: what has been dealt with already.
//
// There is one JSON file per configuration in the service working directory:
//
//	{"received":"2026-09-05T03:00:00","deposited":["<sha1>","<sha1>"]}
//
// The time is that of the last message whose envelope was built, whether or
// not the envelope reached the deposit directory: one that did not is waiting
// in the working directory and will be moved by a later run, so its message
// must not be turned into an envelope a second time.
//
// The digests are those of the messages dealt with at exactly that time.
// Delivery times have a one second resolution, so several messages can share
// one; without the digests, telling those apart would mean counting them, and
// a message that somebody else removes from the mailbox would shift the count
// and carry another one over the watermark unsent.
//
// Deleting the file makes the next run deposit every message of the mailbox.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Local time without a zone, like the mbox separator lines it comes from.
const watermarkTimeLayout = "2006-01-02T15:04:05"

// Watermark is how far the last run got. Its zero value covers nothing, which
// is what a mailbox never deposited from needs.
type Watermark struct {
	Received time.Time

	// Enveloped holds the digests of the messages enveloped at Received,
	// whether or not their envelopes have reached the deposit directory yet.
	Enveloped []string
}

// covers reports whether an earlier run has turned the message into an
// envelope already: everything before Received is covered by the time alone,
// and at Received itself by the digest.
func (w Watermark) covers(key MessageKey) bool {
	if key.Received.Before(w.Received) {
		return true
	}
	if !key.Received.Equal(w.Received) {
		return false
	}
	for _, digest := range w.Enveloped {
		if digest == key.SHA1 {
			return true
		}
	}
	return false
}

// with is the watermark that also covers the message: the later of the two
// times, and the digests of the messages enveloped at that time. Folding it
// over a set of messages therefore leaves the greatest of their delivery
// times, in whatever order they come.
func (w Watermark) with(key MessageKey) Watermark {
	switch {
	case key.Received.After(w.Received):
		// A later message: the digests of the second before it are of no more
		// use, since everything before the time is covered by the time alone.
		return Watermark{Received: key.Received, Enveloped: []string{key.SHA1}}
	case key.Received.Equal(w.Received):
		return Watermark{Received: w.Received, Enveloped: append(w.Enveloped, key.SHA1)}
	}
	return w // an earlier message: covered already
}

// watermarkFile is the watermark as the file spells it. The key is the first
// version's, which deposited each envelope as it built it; it is kept so that
// the watermarks that version wrote still load.
type watermarkFile struct {
	Received  string   `json:"received"`
	Deposited []string `json:"deposited"`
}

func watermarkPath(workDir, name string) string {
	return filepath.Join(workDir, name+".watermark")
}

// readWatermark reads the watermark of a mailbox configuration. A mailbox never
// deposited from has none, and gets the zero watermark.
func readWatermark(workDir, name string) (Watermark, error) {
	path := watermarkPath(workDir, name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Watermark{}, nil
	}
	if err != nil {
		return Watermark{}, err
	}

	var stored watermarkFile
	var received time.Time
	err = json.Unmarshal(data, &stored)
	if err == nil {
		received, err = time.ParseInLocation(watermarkTimeLayout, stored.Received, time.Local)
	}
	if err != nil {
		return Watermark{}, fmt.Errorf("%s: %w", path, err) // names the file to delete, if it is past repair
	}
	return Watermark{Received: received, Enveloped: stored.Deposited}, nil
}

// writeWatermark stores the watermark atomically: a crash can never leave a
// half written one, which would make the next run deposit everything again.
func writeWatermark(workDir, name string, watermark Watermark) error {
	stored := watermarkFile{
		Received:  watermark.Received.Format(watermarkTimeLayout),
		Deposited: watermark.Enveloped,
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return writeFileAtomically(watermarkPath(workDir, name), append(data, '\n'), 0o600)
}
