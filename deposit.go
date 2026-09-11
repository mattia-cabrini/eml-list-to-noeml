// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// Moving the built envelopes into the deposit directory, where
// SimpleQueueMailing picks them up, and who they belong to once there.

package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// envelopePerm is what a deposited envelope is readable by, and nothing more:
// an envelope is signed, not encrypted, so it carries the original message in
// clear. Not even its owner may write it, since there is nothing left to
// change in it once it is deposited.
const envelopePerm = 0o440

// owner is the user and group the deposited envelopes belong to. An id of -1
// means that the configuration named nobody and that ownership is left as it is.
type owner struct {
	user, group string
	uid, gid    int
}

// lookupOwner resolves the two names the configuration gives, either of which
// may be empty, a name or a numeric id. It is the one place that says what an
// acceptable owner is: the installer asks it too, so that what it accepts and
// what the run accepts cannot drift apart.
func lookupOwner(userName, groupName string) (owner, error) {
	own := owner{user: userName, group: groupName, uid: -1, gid: -1}
	var err error

	if userName != "" {
		if own.uid, err = lookupUser(userName); err != nil {
			return owner{}, fmt.Errorf("owner_user = %s: %w", userName, err)
		}
	}
	if groupName != "" {
		if own.gid, err = lookupGroup(groupName); err != nil {
			return owner{}, fmt.Errorf("owner_group = %s: %w", groupName, err)
		}
	}
	return own, nil
}

// String is how the log says who the envelopes will belong to.
func (o owner) String() string {
	if o.uid < 0 && o.gid < 0 {
		return "whoever runs the program"
	}
	return fmt.Sprintf("%s:%s (%d:%d)", o.user, o.group, o.uid, o.gid)
}

// apply gives the file the mode and the ownership a deposited envelope needs.
// The mode is set outright although the file was created with it: creation
// goes through the umask, and a name left over from an earlier attempt keeps
// its old mode when it is opened for writing again.
func (o owner) apply(path string) error {
	if err := os.Chmod(path, envelopePerm); err != nil {
		return err
	}
	return os.Chown(path, o.uid, o.gid)
}

// lookupUser is the id of a user, named or numeric.
func lookupUser(name string) (int, error) {
	if id, err := strconv.Atoi(name); err == nil {
		return id, nil
	}
	found, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(found.Uid)
}

// lookupGroup is the id of a group, named or numeric.
func lookupGroup(name string) (int, error) {
	if id, err := strconv.Atoi(name); err == nil {
		return id, nil
	}
	found, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(found.Gid)
}

// moveInto moves the file at path into dir, under the same name.
//
// The content is copied under the partial name, given its ownership there, and
// takes its own with a rename inside dir, which happens all at once: the name
// appears with the whole envelope behind it or not at all. It is a copy and
// not a rename from the working directory because the two directories may well
// be on different file systems, and a rename cannot cross one.
//
// An error means that nothing was deposited. Once the envelope is there,
// whatever happens to the copy left behind is a matter for the log alone.
func moveInto(dir, path string, own owner) error {
	target := filepath.Join(dir, filepath.Base(path))
	partial := target + partialSuffix

	envelope, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	err = writeFileSynced(partial, envelope, envelopePerm)
	if err == nil {
		err = own.apply(partial) // owner and mode before the name: nobody ever sees it otherwise
	}
	if err == nil {
		err = os.Rename(partial, target)
	}
	if err != nil {
		os.Remove(partial)
		return err
	}
	forgetSource(path, target)
	return nil
}

// forgetSource takes the envelope out of the working directory once it has
// been deposited.
//
// From the rename on, the envelope is the consumer's, and the runs to come
// must never offer it again: one left behind under a name they look for is
// deposited, and sent, once more every minute. So when it cannot be removed it
// is at least set aside under a name they ignore, and when even that fails the
// log says so, because nothing here can stop the repetition on its own.
func forgetSource(path, target string) {
	err := os.Remove(path)
	if err == nil {
		return
	}
	if asideErr := os.Rename(path, path+".deposited"); asideErr != nil {
		logf(ERROR, "deposited %s, but %s can be neither removed (%v) nor set aside (%v):"+
			" every run will deposit and send it again until it is taken away by hand",
			target, path, err, asideErr)
		return
	}
	logf(WARNING, "deposited %s, but %s could not be removed (%v): left as %s.deposited",
		target, path, err, path)
}
