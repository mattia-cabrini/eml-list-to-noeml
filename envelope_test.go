// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestNotice(t *testing.T) {
	message := &Message{
		Key:     MessageKey{Received: sampleDelivery, SHA1: "abc"},
		Subject: "Cron <root@host> backup",
	}
	n := notice{user: "root", host: "host", message: message}

	wantBody := "New message to root@host, received on 2026-09-05 at 03:00:00.\r\n" +
		"\r\n" +
		"The message is attached.\r\n" +
		"\r\n" +
		"--\r\n" +
		"noreply\r\n"
	if got := n.body(); got != wantBody {
		t.Errorf("body:\n%q\nwant:\n%q", got, wantBody)
	}
	if got, want := n.subject(), "New message to root@host: Cron <root@host> backup"; got != want {
		t.Errorf("subject %q, want %q", got, want)
	}
	if got, want := n.attachmentName(), fmt.Sprintf("root-%d.eml", sampleDelivery.Unix()); got != want {
		t.Errorf("attachment name %q, want %q", got, want)
	}
}

func TestQuotedEscapesForCmcEML(t *testing.T) {
	if got, want := quoted("a\"b\\c\r\n"), `"a\"b\\c\r\n"`; got != want {
		t.Errorf("quoted %s, want %s", got, want)
	}
}

func TestTextHeaderFoldsEncodedWords(t *testing.T) {
	if got, want := textHeader("Subject", "plain"), `do=add-header key="Subject" value="plain"`; got != want {
		t.Errorf("ASCII value: %s, want %s", got, want)
	}

	// 120 bytes of UTF-8: more than one encoded word of 45 bytes each.
	folded := textHeader("Subject", strings.Repeat("è", 60))
	if strings.Contains(folded, "?= =?") || !strings.Contains(folded, `?=\r\n =?`) {
		t.Errorf("encoded words not folded: %s", folded)
	}
}
