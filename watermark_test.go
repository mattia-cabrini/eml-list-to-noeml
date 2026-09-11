// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"
)

func TestWatermarkCovers(t *testing.T) {
	second := sampleDelivery
	first := MessageKey{Received: second, SHA1: "aaa"}
	sameSecond := MessageKey{Received: second, SHA1: "bbb"}
	later := MessageKey{Received: second.Add(time.Second), SHA1: "ccc"}
	earlier := MessageKey{Received: second.Add(-time.Second), SHA1: "ddd"}

	empty := Watermark{}
	for _, key := range []MessageKey{first, sameSecond, later, earlier} {
		if empty.covers(key) {
			t.Errorf("a mailbox never deposited from covers nothing, but covers %v", key)
		}
	}

	mark := empty.with(first)
	cases := []struct {
		key  MessageKey
		want bool
	}{
		{first, true},       // the very message it was made of
		{sameSecond, false}, // same second, different message
		{later, false},
		{earlier, true},
	}
	for _, c := range cases {
		if got := mark.covers(c.key); got != c.want {
			t.Errorf("covers %v: %v, want %v", c.key, got, c.want)
		}
	}
	if !mark.with(sameSecond).covers(sameSecond) {
		t.Error("a second message of the same second should be remembered too")
	}
	if moved := mark.with(later); !moved.Received.Equal(later.Received) || len(moved.Enveloped) != 1 {
		t.Errorf("moving to a later second gives %v; it should forget the digests of the second before", moved)
	}
	if back := mark.with(earlier); !back.Received.Equal(mark.Received) || len(back.Enveloped) != 1 {
		t.Errorf("an earlier message gives %v; it is covered already and must not move the watermark", back)
	}
}

func TestWatermarkRoundTrip(t *testing.T) {
	workDir := scratchDir(t)

	empty, err := readWatermark(workDir, "root")
	if err != nil || !empty.Received.IsZero() {
		t.Fatalf("no watermark yet: got %v, %v; want the zero one and no error", empty, err)
	}

	mark := Watermark{}.with(MessageKey{
		Received: sampleDelivery,
		SHA1:     "0123456789abcdef0123456789abcdef01234567",
	})
	if err := writeWatermark(workDir, "root", mark); err != nil {
		t.Fatal(err)
	}
	loaded, err := readWatermark(workDir, "root")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Received.Equal(mark.Received) || len(loaded.Enveloped) != 1 ||
		loaded.Enveloped[0] != mark.Enveloped[0] {
		t.Fatalf("loaded %v, want %v", loaded, mark)
	}
}
