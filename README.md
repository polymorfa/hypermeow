# HyperMeow
[![Go Reference](https://pkg.go.dev/badge/github.com/polymorfa/whatsmeow.svg)](https://pkg.go.dev/github.com/polymorfa/whatsmeow)

HyperMeow is a library used at Polymorfa to ship WhatsApp at scale. We forked from tulir's project since these performance changes are somewhat experimental and diverge from tulir's minimalist philosophy. For Polymorfa to succeed, we needed all the WhatsApp Web functions in one place, meanwhile tulir prefers the core functionalities / messaging be the scope of whatsmeow.

The module path and package names remain `go.mau.fi/whatsmeow` for compatibility. Consumers can test HyperMeow with a Go module `replace` directive while the fork is validated against upstream behavior.

The reproducible Barback and PostgreSQL benchmark is documented in [`benchmark/barback`](benchmark/barback/README.md).

## Discussion

Discord server (#hypermeow channel): https://whiskey.so/discord

## Usage

The [godoc](https://pkg.go.dev/github.com/polymorfa/whatsmeow) includes docs for all methods and event types.
There's also a [simple example](https://pkg.go.dev/github.com/polymorfa/whatsmeow#example-package) at the top.

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
