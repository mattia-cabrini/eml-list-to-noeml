// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// The `purge` command: remove program, configuration, cron job and watermarks.
// The deposit directory is left alone: it belongs to SimpleQueueMailing.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func purge() error {
	targets := []string{installed.cronFile, installed.binary, installed.etcDir}
	targets = append(targets, workDirsToRemove()...)

	var existing []string
	for _, target := range targets {
		if _, err := os.Lstat(target); err == nil {
			existing = append(existing, target)
		}
	}
	if len(existing) == 0 {
		fmt.Println("Nothing to remove.")
		return nil
	}

	fmt.Println("About to remove:")
	for _, target := range existing {
		fmt.Printf("  %s\n", target)
	}
	if !confirm("Proceed?") {
		fmt.Println("Nothing removed.")
		return nil
	}

	for _, target := range existing {
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		fmt.Printf("Removed %s\n", target)
	}
	return nil
}

// workDirsToRemove are the working directories named by the service
// configuration and by the mailbox configurations.
//
// Only a directory whose name says that it is ours is removed: these paths
// come from configuration files where a mistake, work_dir = /var/lib, would
// otherwise cost the whole of somebody else's directory.
func workDirsToRemove() []string {
	service, err := readServiceConfig(installed.serviceConfig)
	if err != nil {
		return nil
	}

	candidates := []string{service.WorkDir}
	if configs, err := mailboxConfigFiles(service); err == nil {
		for _, path := range configs {
			if mailbox, err := readMailboxConfig(path); err == nil && mailbox.WorkDir != "" {
				candidates = append(candidates, mailbox.WorkDir)
			}
		}
	}

	var ours []string
	seen := map[string]bool{}
	for _, dir := range candidates {
		switch {
		case seen[dir]:
		case !strings.Contains(filepath.Base(dir), programName):
			fmt.Printf("Not removing the working directory %s: its name does not say that it is ours."+
				" Remove it by hand if it really is.\n", dir)
		default:
			ours = append(ours, dir)
		}
		seen[dir] = true
	}
	return ours
}
