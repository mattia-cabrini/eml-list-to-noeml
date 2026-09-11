<!--
Copyright (c) 2026 Mattia Cabrini
SPDX-License-Identifier: MIT
-->

# eml-list-to-noeml

Turns a Unix mailbox such as `/var/mail/root`, a file of concatenated messages in
mbox format, into one signed `.noeml` envelope per message, ready to be sent by
[SimpleQueueMailing](https://github.com/mattia-cabrini/SimpleQueueMailing).
It runs from cron every minute and only handles the messages that have not been
deposited yet.

Every envelope is an OpenPGP/MIME signed e-mail that carries the original
message as an attachment:

```
New message to root@host, received on 2026-09-05 at 03:00:00.

The message is attached.

--
noreply
```

Written in Go with the standard library only. Works on Ubuntu Server and on
FreeBSD (12 or later).

## How it works

A run has two phases, in [run.go](run.go):

1. **The service configuration** is read. What the run will do with the
   envelopes it deposits, the directory and the ownership, goes to the log at
   level INFO, so that a later error can be read against it. An unusable
   configuration is logged at level ERROR and the run stops.
2. **The mailbox configurations** are taken one at a time. What goes wrong with
   one of them concerns that one only: the others are processed anyway.

Each mailbox configuration goes through four phases of its own:

1. **The configuration and the watermark** are read. The file being converted
   goes to the log at level INFO; an unusable configuration, at level ERROR.
2. **The mailbox is read, and the unsigned envelope of each new message written.**
   [MailboxReader](reader.go) is a state machine over the lines of the file:
   whatever precedes the first separator line is not a message, every separator
   line opens one, and the next separator line or the end of the file closes
   it. A separator line is one that begins with `From ` and carries a delivery
   time that parses, so a line of prose beginning the same way cannot cut a
   message in two; a line like that is kept with the message it turned out to
   belong to and written to the log when that message is handed out, once, and
   not at every reading of the mailbox. `Next` hands out one message at a time
   and discards the ones the watermark covers. The file is not read line by
   line but through a cache
   of one fixed size, 64 MiB, filled over and over rather than grown: what has
   been read out of it is already in a message, so nothing left in it is worth
   keeping, and what the reader holds does not follow the size of the mailbox.
   The pages a read never reaches are never touched, so a small mailbox costs
   what a small mailbox should. The read that reaches the end of the file gives
   the mailbox back at once, and the cache goes with it. Each message handed
   out is turned there and then into its unsigned envelope on disk, the thing
   that is going to be signed, and its bytes are let go: a first run over a
   mailbox left to grow for years costs the memory of one message and not of
   the backlog.
3. **The unsigned envelopes are signed, once the mailbox has been given back.**
   `gpg` signs each, `cmc-eml` seals it with its signature under the envelope
   headers, and the result takes the name the deposit looks for. This phase
   holds no lock and needs no message in memory, which is why it comes after
   the reading and not during it: signing a backlog takes minutes, and the
   mailbox must not be held for them.
4. **The envelopes are deposited and the watermark is updated.** Every envelope
   waiting in the working directory is moved into the deposit directory, owned
   and readable as the configuration says. The ones a previous run could not
   move are waiting there too, and are moved now. Each is copied to a name the
   consumer ignores and takes its own with a rename inside the deposit
   directory, so that the two directories may sit on different file systems,
   where a rename could not reach. The watermark then reaches the last message
   whose envelope was built, whether or not that envelope was moved.

### The workflow, and what each phase can say

```
run SERVICE_CONFIG
├─ open the system log                               WARN   neither the log socket nor logger(1) can be
│                                                           reached: writing on standard error
├─ read the service configuration                    ERROR  the file, or a value in it, is unusable
│                                                    ERROR  owner_user or owner_group names nobody
│                                                    INFO   depositing into DIR, as USER:GROUP, mode 0440
├─ take the run lock, WORK_DIR/run.lock              ERROR  the lock file cannot be opened or locked
│                                                    WARN   the previous run is still going: skipping this one
├─ find the mailbox configurations                   ERROR  the include glob is malformed
│                                                    WARN   no mailbox configuration matches GLOB
└─ for each mailbox configuration:
   │
   ├─ 1. read the configuration and the watermark    ERROR  the file, or a value in it, is unusable
   │                                                 ERROR  the watermark cannot be read
   │                                                 INFO   NAME: converting FILE
   │
   ├─ 2. open the mailbox, taking its locks          DEBUG  there is no FILE yet
   │  │                                              WARN   removing the lock FILE.lock, left behind more than 5m0s ago
   │  │                                              WARN   FILE in use by another program, retrying next run
   │  │                                              ERROR  the mailbox cannot be opened or locked
   │  │                                              WARN   byte N, LINE: no delivery time on it, and it comes
   │  │                                                     before the first message
   │  └─ for each message the reader hands out:      ERROR  reading FILE failed
   │        skip it if the watermark covers it       DEBUG  already enveloped: MESSAGE
   │        report the From lines of its body        WARN   byte N, LINE: no delivery time on it, so it does
   │          that do not start a message                   not start a message
   │        skip it if an identical one came before  WARN   MESSAGE is there twice, byte for byte
   │        write NAME.noeml.part.unsigned           ERROR  cannot write out MESSAGE (stops this mailbox)
   │        let go of the message's bytes            DEBUG  written out: NAME.noeml
   │
   ├─ 3. for each unsigned envelope written:         ERROR  cannot sign the envelope of MESSAGE (stops this mailbox)
   │        gpg signs it, cmc-eml seals it           DEBUG  signed NAME.noeml
   │        it becomes NAME.noeml
   │     sweep what was left half made               DEBUG  removing what was left of an envelope: PATH
   │
   └─ 4. list the NAME.noeml waiting                 ERROR  cannot list the envelopes waiting in DIR
         for each of them:                           WARN   could not deposit PATH into DIR
            copy it into the deposit directory       INFO   deposited NAME.noeml
            give it its owner and mode 0440          WARN   deposited NAME, but its copy could not be removed
            rename it to NAME.noeml                  ERROR  deposited NAME, but its copy can be neither
            remove the copy left behind                     removed nor set aside
                                                     INFO   N envelope(s) of M deposited
         update the watermark                        ERROR  the watermark cannot be written
```

An envelope therefore takes three names in the working directory before it
leaves it, and each is taken with a rename, so that a pass that dies half way
leaves nothing carrying the name of a finished thing:

```
1757041200_root@host_da39a3ee....noeml.part.unsigned   the unsigned envelope, what gpg signs
1757041200_root@host_da39a3ee....noeml.part            the signed envelope, while it is written
1757041200_root@host_da39a3ee....noeml                 the envelope, ready to be deposited
```

The working directory also holds the watermarks, `run.lock`, which stays there
between runs and is harmless, and whatever the deposit had to set aside as
`<name>.noeml.deposited`. None of those ends in `.noeml`, which is the one name
the deposit looks for.

### The envelope names

An envelope is named after the message it carries, and only after it:

```
<delivery time>_<sender>_<sha1 of the message>.noeml
1757041200_root@host_da39a3ee5e6b4b0d3255bfef95601890afd80709.noeml
```

The delivery time is the one on the `From ` line, in seconds since the epoch.
The sender comes from the `From` header, or from the `From ` line when the
message has none, reduced to the bare address, to the characters that are
harmless in a file name and to 64 of them at most; a message with no sender at
all gets `unknown`. The digest covers the whole
message, headers and body, with the mbox packaging left out: the `From `
separator line and the blank line that closes the message. A last message the
file leaves without a final newline is taken as having one, so that its digest
does not change when the next delivery is appended after it. Two messages
differing in a single header are two messages.

Building the same message twice therefore writes the same file twice, instead
of depositing the message twice.

### The watermark

The watermark is one JSON file per mailbox configuration, in the service working
directory (`work_dir` of `service.conf`), even for a mailbox whose own `work_dir`
builds its envelopes elsewhere:

```
/var/lib/eml-list-to-noeml/root.watermark
{"received":"2026-09-05T03:00:00","deposited":["<sha1>","<sha1>"]}
```

The time is that of the last message whose envelope was built, whether or not
that envelope reached the deposit directory: one that did not is waiting in the
working directory, and a later run moves it from there rather than building it
again.

The digests are those of the messages dealt with at exactly that time. Delivery
times have a one second resolution, so several messages can share one; telling
them apart by their content, rather than by counting them, is what keeps a
message that somebody else removes from the mailbox from shifting the identity
of the others.

Delete the file to have every message still in the mailbox deposited again.

### The envelope

```
multipart/signed; protocol="application/pgp-signature"; micalg=pgp-sha256
├── multipart/mixed                   the signed part
│   ├── text/plain                    the notice above
│   └── message/rfc822 (base64)       the original message, attached as root-<delivery time>.eml
└── application/pgp-signature         detached signature
```

The envelope carries `To`, `Subject` (`New message to root@host: <original subject>`),
`Date`, `Message-ID` and `Auto-Submitted`; SimpleQueueMailing adds `From`.
In `root@host`, `root` is the name of the mailbox file and `host` the `hostname`
of the service configuration, or the name this machine calls itself when it
names none.

## Requirements

- Go 1.18 or later to build; the program itself has no dependencies.
- [cmc-eml](https://github.com/mattia-cabrini/cmc-eml) in `PATH`.
- `gpg` (GnuPG 2.1 or later), with the signing key in root's keyring.
- cron: Ubuntu's `cron` package, or FreeBSD's base cron (12 or later, which
  reads `/usr/local/etc/cron.d`).
- A running system log: rsyslog or journald on Ubuntu, syslogd on FreeBSD.
  `logger(1)`, which both systems have in the base, is the second way into it.
- A running SimpleQueueMailing whose input queue is the deposit directory.

Everything runs as root, because mailboxes in `/var/mail` are only readable by
their owner and root.

## Build and install

```sh
make build          # as a regular user: root does not need the Go toolchain
make test           # a few unit tests; they need no external program
sudo make install
```

The tests read their fixtures from `testdata/` and write only under `ignore/`,
inside the repository, which git does not track. Reading a mailbox creates a
lock file next to it, so they read a copy of the fixture made there.

The installer asks for the service configuration, proposing defaults, and
writes it, copies the program and schedules the cron job. The directories the
service works in, the deposit directory, the working directory and the one the
mailbox configurations go in, are never created behind your back: you are
asked, the default answer is no, and a no leaves the directory to you (until
`make config` needs the last one, and creates it to write into it). The
directories of the program, of the service configuration and of the cron job
are created as needed. Run the installer again to update
the program or to change the configuration: the current values are proposed
instead of the defaults. Moving the working directory does not move the
watermarks: copy the `*.watermark` files over, or every message is deposited
again.

**Upgrading from the first version.** `owner_user` and `owner_group` are
required, so a `service.conf` written before them makes every run fail with
`missing owner_user, owner_group` until `make install` is run again. The
watermark files themselves are read as they are and need no attention.

| What | Ubuntu Server | FreeBSD |
| --- | --- | --- |
| Program | `/usr/local/bin/eml-list-to-noeml` | same |
| Service configuration | `/etc/eml-list-to-noeml/service.conf` | `/usr/local/etc/eml-list-to-noeml/service.conf` |
| Mailbox configurations (default) | `/etc/eml-list-to-noeml/conf.d/*.conf` | `/usr/local/etc/eml-list-to-noeml/conf.d/*.conf` |
| Working directory (default) | `/var/lib/eml-list-to-noeml/` | `/var/db/eml-list-to-noeml/` |
| Deposit directory (default) | `/var/spool/noeml/` | same |
| Cron job | `/etc/cron.d/eml-list-to-noeml` | `/usr/local/etc/cron.d/eml-list-to-noeml` |

The service configuration is a list of `key = value` lines, as the installer
writes it:

```
# Glob matching the mailbox configuration files; "eml-list-to-noeml config" adds them.
include = /etc/eml-list-to-noeml/conf.d/*.conf
# Where the .noeml envelopes are deposited: the input queue of SimpleQueueMailing.
output_dir = /var/spool/noeml
# Watermarks, and the envelopes while they are built.
work_dir = /var/lib/eml-list-to-noeml
# The deposited envelopes belong to this user and this group, and are read only
# for them and unreadable for everybody else: an envelope is signed, not
# encrypted, so it carries the original message in clear. Leave a name empty to
# leave that one as it is.
owner_user = root
owner_group = external-log
# The name this machine goes by in the envelopes; empty means the system one.
hostname = mail.example.com
# How much reaches the log: 0 FATAL, 1 ERROR, 2 WARNING, 3 INFO, 4 DEBUG.
log_level = 3
```

The envelopes say which mailbox on which machine got a message, and `hostname`
is that name. The installer proposes what this machine calls itself, which is
not always the name the reader of the envelope knows it by; leave the value out
or empty to have it looked up at every run instead.

An envelope is signed, not encrypted: it carries the original message in clear.
It is therefore deposited with mode 0440, read only for its owner and its
group, unreadable for anybody else, and given to `owner_user` and
`owner_group`, by default `root` and `external-log`. The working directory and
the deposit directory are created with mode 0700 for the same reason.
SimpleQueueMailing runs as root and reads and removes the envelopes as root, so
the group is a label saying what they are rather than a way in.

The cron job runs every minute. The program writes into the system log itself,
and the job pipes its standard error into `logger` all the same, so that what
the program cannot route there on its own — a panic of the Go runtime — is not
lost either. Cron's own mailing is disabled, since it would deliver to root's
mailbox, which is most likely one of the files being converted.

## Configure a mailbox

First put the signing key in root's keyring and its passphrase in a file that
only root can read:

```sh
sudo gpg --import signing-key.asc
sudo sh -c 'umask 077; echo "the passphrase" > /etc/eml-list-to-noeml/root.passphrase'
```

Then add the configuration:

```sh
sudo make config
```

It asks for a name, the mailbox file, the recipient, the GPG key, the
passphrase file and the working directory, and writes the file where the
`include` glob looks, with the extension the glob asks for, by default
`<include directory>/<name>.conf`:

```
# The mbox file to convert.
file = /var/mail/root
# Who receives the messages: one or more e-mail addresses, comma separated, no display names.
recipient = admin@example.com
# The GPG key that signs them: key id, fingerprint or e-mail of a key in root's keyring.
gpg_key = admin@example.com
# File whose first line is the passphrase of that key.
gpg_passphrase_file = /etc/eml-list-to-noeml/root.passphrase
# Where the envelopes of this mailbox are built, before being deposited.
work_dir = /var/lib/eml-list-to-noeml
```

Run `make config` again with the same name to change it: the current values are
proposed. The next run of the service picks the file up; there is a watermark
per configuration, named after it.

## Running by hand and logs

```sh
sudo eml-list-to-noeml run /etc/eml-list-to-noeml/service.conf
```

The program writes into the system log itself, under the tag
`eml-list-to-noeml` and the daemon facility, whether it was started by cron or
by hand. There is nothing to watch on the terminal: watch the log instead.

```sh
journalctl -ft eml-list-to-noeml           # Ubuntu
tail -f /var/log/messages                  # FreeBSD
```

Every line carries the time, ISO 8601 with the zone, and its level in front of
it, wherever it lands:

```
2026-09-05T03:01:00+02:00 [ INFO    ] depositing into /var/spool/noeml, as root:external-log (0:2000), mode 0440
2026-09-05T03:01:00+02:00 [ INFO    ] root: converting /var/mail/root
2026-09-05T03:01:02+02:00 [ INFO    ] root: deposited 1757041200_root@host_da39a3ee....noeml
2026-09-05T03:01:02+02:00 [ INFO    ] root: 1 envelope(s) of 1 deposited
```

A line has to reach the log whatever the state of the machine, since there is
nowhere else to look, so there are two ways in and each line goes to the first
one that takes it:

1. the syslog library, which writes to the log socket itself and holds it open
   for the run;
2. `logger(1)`, the system's own tool, for the machine where that socket cannot
   be opened — a container without `/dev/log`, a host that only logs over the
   network. It costs a process per line, which is why it comes second.

Standard error is what is left when even that fails, and the cron job pipes it
into `logger`, so a line still lands in the log. A run started by hand writes it
on the terminal, where you can see it.

`log_level` says how much is written: 0 FATAL, 1 ERROR, 2 WARNING, 3 INFO
(the default), 4 DEBUG. At level 4 every message read and discarded because the
watermark covers it is written too, and so is every file removed by the sweep.

What the program calls INFO enters the system log as a notice rather than as an
informational message, on purpose: FreeBSD writes `daemon.notice` and above to
`/var/log/messages` by default, and what the program says at INFO is what the
operator has to be able to read there. DEBUG stays below that line, where it
belongs, and is not written to `/var/log/messages` unless `syslog.conf` says so.

A run that finds another run still going, or a mailbox held by another program,
writes a WARNING and leaves the work to the next minute.

### Dry run

```sh
eml-list-to-noeml dry-run /var/mail/root /tmp/noeml
```

Converts the whole of a mailbox, watermark or not, into plain envelopes, the
same headers, notice and attachment as the service makes but unsigned and
addressed to `someone@example.com`, and deposits them into the directory given,
mode 0440, owned by whoever runs it. It reads no configuration and needs
nothing installed but `cmc-eml`, so it runs on any machine as any user who can
read the mailbox; nothing of the service is touched, no watermark read or
written, no run lock taken. The envelopes are built in a directory of their own
inside the one given, `.eml-list-to-noeml.build`, which goes away at the end.
The log stays on the terminal.

## Remove

```sh
sudo make purge
```

Lists what it is about to remove (cron job, program, configuration directory
with the mailbox configurations and any passphrase file inside it, the service
working directory with the watermarks and the run lock, the working directories
of the mailboxes) and asks for confirmation. A working
directory whose name does not say that it is ours is only reported, never
removed: the path comes from a configuration file, where a mistake would
otherwise cost somebody else's directory. Deposited envelopes are left alone.

## Repository layout

```
Makefile         build / test / install / config / purge / clean, for GNU make and BSD make
go.mod           module definition, no dependencies
main.go          the sub-commands
run.go           the run command: the phases
log.go           the levels, their prefixes, and the system log
settings.go      the configuration files
message.go       one message: its key, its name, its headers
reader.go        the state machine that reads a mailbox
lock.go          the run lock and the mailbox locks
envelope.go      the two passes that make an envelope, and the names it takes
deposit.go       moving the envelopes, and who they belong to
watermark.go     what has been dealt with already
atomicfile.go    writing a file so that it appears all at once
setup.go         installed paths (per operating system), prompts, how an installed file is written
install.go       the install command
addconfig.go     the config command
purge.go         the purge command
dryrun.go        the dry-run command: a whole mailbox into plain envelopes, in a directory of your choice
*_test.go        unit tests: reading a mailbox and the configuration files, envelope names,
                 the notice and the cmc-eml commands, the watermark; helpers_test.go is what they share
testdata/        the mailbox and the configuration files the tests read
```

## Notes

- **A message is deposited at least once.** A crash between the move of an
  envelope and the update of the watermark makes the next run build and deposit
  that envelope again. Its name is the same, so an envelope that
  SimpleQueueMailing has not taken yet is merely overwritten; one that it has
  already taken and sent is sent a second time. Closing this needs the consumer
  to say what it has taken, which the `.noeml` protocol does not provide.
- **The mailbox is expected to grow at the end.** Every run reads the whole
  file and discards what the watermark covers, so removing a message from it is
  harmless, and so is rewriting one in place, a `Status` header added by a mail
  client, say: every message delivered before the second the watermark stands
  at is covered by its time alone. Only a message rewritten in that very second
  changes its digest, and is deposited again.
- **The delivery time is local and has no zone**, like the `From ` line it comes
  from. Messages are ordered by it, so a clock that goes backwards (the end of
  daylight saving time, a large NTP step) can leave the messages delivered in
  the repeated interval behind the watermark. Delete the watermark to have them
  deposited.
- **A message that cannot be made into an envelope stops its mailbox.** It is written to the
  log at level ERROR, with the file it comes from, its sender, its recipient
  and its delivery time; the messages before it are deposited, the ones after
  it wait for the next run. It is not skipped: skipping it would lose it
  silently.
- **An envelope that cannot be moved is not blocking.** It is written at level
  WARNING, with the path and the reason, and the other envelopes are moved all
  the same. It stays in the working directory and the next run moves it from
  there; it is not built again, since the watermark already covers its message.
  Depositing is a copy followed by the removal of the copy left behind, so a
  removal that fails leaves the envelope where the next run would find it. It
  is therefore set aside as `<name>.noeml.deposited`, a name no run looks for,
  and the log says which file that was: it is finished with, and can be deleted
  by hand. When even setting it aside fails, the log says at level ERROR that
  the envelope will be deposited and sent again every run until somebody takes
  it away.
  Emptying the working directory by hand therefore loses whatever is waiting in
  it, unless the watermark goes with it. Two configurations that share a
  working directory share what waits in it: either may deposit the other's
  leftovers, which costs nothing but reads oddly in the log.
- **One mailbox file, one configuration.** An envelope is named after the
  message alone, so two configurations converting the same file, to two
  different recipients, would build the same name twice and overwrite each
  other. Give each mailbox file one configuration, and list the several
  recipients in it.
- **The mailbox locks are advisory**, and all three protocols in use are taken:
  the dot lock, `<mailbox>.lock`, which Postfix takes to deliver and which mutt
  and mail take as well; flock, taken by the mail user agents; and the fcntl
  lock, which Postfix takes on Linux, where it is a lock space of its own that
  flock does not meet. On FreeBSD flock and fcntl are the same kernel lock. A
  dot lock left behind by a program that died is removed after five minutes,
  with a WARNING in the log, which is under the 500 seconds Postfix waits
  before breaking one of its own.
- **Two messages that are the same byte for byte, headers included, delivered
  in the same second, become one envelope.** Nothing tells them apart: the
  envelope is named after the delivery time and the digest of the whole
  message. It takes a sender that writes neither `Message-ID` nor `Date`, which
  no mail transfer agent does. It is written to the log at level WARNING.
- The attachment is `message/rfc822` encoded in base64. RFC 2046 forbids that
  encoding for the type, but base64 is what keeps the signature valid across
  transport and the common mail clients cope. `attachmentMIMEType` in
  [envelope.go](envelope.go) switches it to `application/octet-stream`.
- The recipient must be a bare e-mail address, or a comma separated list of
  them: SimpleQueueMailing hands the value to the SMTP server as it is.
- Subjects longer than 200 characters are cut, because `cmc-eml` reads each
  command into a 4 KiB buffer. A subject that is not ASCII is RFC 2047 encoded
  and folded over several header lines.

## License

MIT, see [LICENSE](LICENSE).
