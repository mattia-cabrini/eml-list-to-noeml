# Copyright (c) 2026 Mattia Cabrini
# SPDX-License-Identifier: MIT
#
# Works with both GNU make (Ubuntu) and BSD make (FreeBSD): keep it plain.
# install, config and purge have to run as root. Build first as a regular user
# (`make build`), so that root does not need the Go toolchain.

GO ?= go
BIN = bin/eml-list-to-noeml

.PHONY: build test install config purge clean

build: $(BIN)

$(BIN): *.go go.mod
	mkdir -p bin
	$(GO) build -o $(BIN) .

test:
	$(GO) test .

# Install or update the service: asks for its configuration, proposing the
# current values when already installed, and schedules a cron job every minute.
install: $(BIN)
	$(BIN) install

# Add a mailbox configuration, or update an existing one.
config: $(BIN)
	$(BIN) config

# Remove program, configuration, cron job and watermarks.
purge: $(BIN)
	$(BIN) purge

clean:
	rm -rf bin
