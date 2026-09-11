// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"testing"
)

func TestEnvelopeName(t *testing.T) {
	digest := "0123456789abcdef0123456789abcdef01234567"
	message := &Message{
		Key:  MessageKey{Received: sampleDelivery, SHA1: digest},
		From: "Cron Daemon <root@host>",
	}

	want := fmt.Sprintf("%d_root@host_%s.noeml", sampleDelivery.Unix(), digest)
	if got := message.EnvelopeName(); got != want {
		t.Errorf("name %q, want %q", got, want)
	}

	// A sender that is not an address, and one that is not there at all.
	message.From = "root at host/../etc"
	if got, want := message.EnvelopeName(), fmt.Sprintf("%d_root_at_host_.._etc_%s.noeml", sampleDelivery.Unix(), digest); got != want {
		t.Errorf("name %q, want %q", got, want)
	}
	message.From = ""
	if got, want := message.EnvelopeName(), fmt.Sprintf("%d_unknown_%s.noeml", sampleDelivery.Unix(), digest); got != want {
		t.Errorf("name %q, want %q", got, want)
	}
}
