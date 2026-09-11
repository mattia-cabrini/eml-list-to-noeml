// Copyright (c) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

// eml-list-to-noeml converts the new messages of mbox files, such as
// /var/mail/root, into signed .noeml envelopes for SimpleQueueMailing. The
// sub-commands are the ones usage lists.
package main

import (
	"errors"
	"fmt"
	"os"
)

const usage = `usage:
  eml-list-to-noeml run SERVICE_CONFIG   convert the new messages (what the cron job runs)
  eml-list-to-noeml install              install or update the service (root)
  eml-list-to-noeml config               add or update a mailbox configuration (root)
  eml-list-to-noeml purge                remove program, configuration, cron job and watermarks (root)
`

func main() {
	setupCommands := map[string]func() error{"install": install, "config": configureMailbox, "purge": purge}

	switch {
	case len(os.Args) == 3 && os.Args[1] == "run":
		if !run(os.Args[2]) {
			os.Exit(1) // what went wrong is in the log already
		}
	case len(os.Args) == 2 && setupCommands[os.Args[1]] != nil:
		if err := asRoot(setupCommands[os.Args[1]]); err != nil {
			fatalf("%v", err)
		}
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

// asRoot runs a command that changes the system, which only root may do.
func asRoot(command func() error) error {
	if os.Geteuid() != 0 {
		return errors.New("this has to run as root: try `sudo make ...` or become root first")
	}
	return command()
}
