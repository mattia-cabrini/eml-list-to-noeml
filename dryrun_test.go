// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The dry run over an empty mailbox goes through every step but cmc-eml, which
// the tests do not have: the checks, the build directory, the reading under
// lock, the deposit of nothing. The two checks on the arguments are errors,
// not warnings per envelope.
func TestDryRunPlumbing(t *testing.T) {
	dir := scratchDir(t)
	empty := filepath.Join(dir, "empty.mbox")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := dryRun(empty, dir); err != nil {
		t.Errorf("an empty mailbox: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "."+programName+".build")); err == nil {
		t.Error("the build directory should be gone at the end")
	}
	if err := dryRun(filepath.Join(dir, "missing.mbox"), dir); err == nil {
		t.Error("a mailbox that is not there should be an error")
	}
	if err := dryRun(empty, filepath.Join(dir, "missing")); err == nil {
		t.Error("a deposit directory that is not there should be an error")
	}
}
