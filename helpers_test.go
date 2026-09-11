// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// What the tests share.

package main

import (
	"os"
	"testing"
	"time"
)

// sampleDelivery is when both messages of testdata/sample.mbox were delivered;
// the other tests use it as the time of any message.
var sampleDelivery = time.Date(2026, 9, 5, 3, 0, 0, 0, time.Local)

// scratchDir is a fresh directory for a test to write into. It lives inside
// the repository, under ignore/ which git does not track, rather than in the
// system temporary directory, and goes away with the test.
func scratchDir(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll("ignore", 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("ignore", "test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
