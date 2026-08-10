# HyperMeow
[![Go Reference](https://pkg.go.dev/badge/github.com/polymorfa/hypermeow.svg)](https://pkg.go.dev/github.com/polymorfa/hypermeow)

HyperMeow is a library used at Polymorfa to ship WhatsApp at scale. We forked from tulir's project since these performance changes are somewhat experimental and diverge from tulir's minimalist philosophy. For Polymorfa to succeed, we needed all the WhatsApp Web functions in one place, meanwhile tulir prefers the core functionalities / messaging be the scope of whatsmeow.

HyperMeow is its own Go module, imported directly as `github.com/polymorfa/hypermeow`. It no longer requires a `replace` directive:

```go
import whatsmeow "github.com/polymorfa/hypermeow"
```

```sh
go get github.com/polymorfa/hypermeow
```

Package names are unchanged from upstream (the root package is still `whatsmeow`), so only import paths differ. Import the root package under its package name, as above, when your tooling expects the path's last element to match.

Migrating from the previous `replace go.mau.fi/whatsmeow => github.com/polymorfa/hypermeow` setup: drop the `replace` line, add a normal `require` on `github.com/polymorfa/hypermeow`, and rewrite `go.mau.fi/whatsmeow` import paths to `github.com/polymorfa/hypermeow`. A `replace` directive is only honoured in the main module, so the previous arrangement did not carry to anything that depended on your module in turn; a direct requirement does.

The reproducible Barback and PostgreSQL benchmark is documented in [`benchmark/barback`](benchmark/barback/README.md).

## Discussion

Discord server (#hypermeow channel): https://whiskey.so/discord

## Usage

The [godoc](https://pkg.go.dev/github.com/polymorfa/hypermeow) includes docs for all methods and event types.
There's also a [simple example](https://pkg.go.dev/github.com/polymorfa/hypermeow#example-package) at the top.

## Features

Most core features are already present:

* Sending messages to private chats and groups (both text and media)
* Receiving all messages
* Managing groups and receiving group change events
* Joining via invite messages, using and creating invite links
* Sending and receiving typing notifications
* Sending and receiving delivery and read receipts
* Reading and writing app state (contact list, chat pin/mute status, etc)
* Sending and handling retry receipts if message decryption fails
* Sending status messages (experimental, may not work for large contact lists)

Things that are not yet implemented:

* Sending broadcast list messages (this is not supported on WhatsApp web either)
* Calls
