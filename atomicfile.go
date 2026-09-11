// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// Files that appear with their whole content at once.

package main

import "os"

// partialSuffix marks a file still being written: "<name>.part" is what the
// writer fills, and renames to <name> once it is whole. Whoever looks for
// finished files, the deposit for the envelopes and SimpleQueueMailing for its
// queue, never looks for this name, so nobody ever reads half of anything.
const partialSuffix = ".part"

// writeFileAtomically writes data to the file at path so that nobody ever sees
// it half written: the data goes to "<path>.part" next to it, which is flushed
// to disk and then renamed over path. A crash leaves at most that file behind.
func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	partial := path + partialSuffix
	err := writeFileSynced(partial, data, perm)
	if err == nil {
		err = os.Rename(partial, path)
	}
	if err != nil {
		os.Remove(partial)
	}
	return err
}

// writeFileSynced writes data to the file at path and waits for it to reach the disk.
func writeFileSynced(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
