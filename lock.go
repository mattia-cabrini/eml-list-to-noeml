// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// The locks: one keeps two runs from overlapping, the other keeps the mailbox
// still while it is read.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// errBusy reports that somebody else holds what was asked for: a mailbox, or
// the run lock. It is the one error the run answers by coming back next time.
var errBusy = errors.New("in use by another program")

// dotLockStaleAfter is how long a dot lock is respected. A program that dies
// between creating one and removing it would otherwise block the mailbox for
// good.
//
// Five minutes is far more than a mailbox is ever held here, since the lock is
// given back as soon as the file has been read and before any envelope is
// built, and it is under the 500 seconds after which Postfix breaks a lock of
// its own.
const dotLockStaleAfter = 5 * time.Minute

// mailboxLock is a mailbox held still. Nothing can stop a program that takes
// no lock at all, but all three protocols the programs touching a mailbox use
// on Ubuntu and on FreeBSD are taken:
//
//   - the dot lock, "<mailbox>.lock" created exclusively, which Postfix takes
//     to deliver (mailbox_delivery_lock) and which mutt and mail take as well;
//   - flock, taken by the mail user agents;
//   - the fcntl lock, which Postfix takes on Linux, where it is a lock space
//     of its own that flock does not meet. On FreeBSD the two are the same
//     kernel lock, so asking for both simply asks twice.
type mailboxLock struct {
	file    *os.File
	dotLock string
}

func lockMailbox(path string) (*mailboxLock, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	lock := &mailboxLock{file: file, dotLock: path + ".lock"}
	if err := createDotLock(lock.dotLock); err != nil {
		file.Close()
		return nil, err
	}
	if err := lockDescriptor(file); err != nil {
		lock.release() // could not hold it: give the mailbox back
		return nil, err
	}
	return lock, nil
}

// release gives the mailbox back. Both descriptor locks go with the descriptor.
func (l *mailboxLock) release() {
	l.file.Close()
	os.Remove(l.dotLock)
}

// lockDescriptor takes the two advisory locks that live on the open file. They
// are shared locks: several readers are welcome, a writer is not.
func lockDescriptor(file *os.File) error {
	if err := flock(file, syscall.LOCK_SH); err != nil {
		return err
	}
	shared := syscall.Flock_t{Type: syscall.F_RDLCK, Whence: 0, Start: 0, Len: 0} // the whole file
	if err := syscall.FcntlFlock(file.Fd(), syscall.F_SETLK, &shared); err != nil {
		return busyOr(err)
	}
	return nil
}

// flock takes an advisory lock on the open file without waiting, and says
// errBusy when somebody else holds it.
func flock(file *os.File, how int) error {
	return busyOr(syscall.Flock(int(file.Fd()), how|syscall.LOCK_NB))
}

// createDotLock creates the file whose sole existence says that the mailbox is
// taken.
func createDotLock(path string) error {
	err := createExclusively(path)
	if !errors.Is(err, os.ErrExist) {
		return err
	}

	info, statErr := os.Stat(path)
	if statErr != nil || time.Since(info.ModTime()) < dotLockStaleAfter {
		return errBusy
	}
	logf(WARNING, "removing the lock %s, left behind more than %s ago", path, dotLockStaleAfter)
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := createExclusively(path); err != nil {
		return errBusy // somebody was quicker
	}
	return nil
}

func createExclusively(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
}

// busyOr turns the ways the kernel has of saying "somebody else holds it" into
// errBusy, and leaves any other error as it is. EWOULDBLOCK is EAGAIN on both
// systems, and a refused fcntl lock may come as either EAGAIN or EACCES.
func busyOr(err error) error {
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EACCES) {
		return errBusy
	}
	return err
}

// lockRun keeps two runs from overlapping: they would build and deposit the
// same envelopes twice. It returns the lock file, which holds the lock until
// it is closed, or errBusy when another run has it.
func lockRun(workDir string) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Join(workDir, "run.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock(lock, syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}
