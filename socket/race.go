// Copyright (c) 2026 Rajeh Taher
// Copyright (c) 2026 Polymorfa (Softium LLC)
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package socket

import (
	"context"
	"errors"
	"net/http"

	"github.com/coder/websocket"

	waLog "github.com/polymorfa/hypermeow/util/log"
)

// LoserSocketCloseReason is the close reason WhatsApp Web sends on the socket
// that loses the race (WAWebOpenSocket.js:47). The code is 1000.
const LoserSocketCloseReason = "loser socket"

// RaceURLs are the two endpoints WhatsApp Web opens concurrently on every
// connect (WAWebOpenSocket.js:10). The second exists because some networks
// block or throttle 443 for long-lived upgrades, and which one wins is a
// property of the network the client is on.
var RaceURLs = []string{
	"wss://web.whatsapp.com/ws/chat",
	"wss://web.whatsapp.com:5222/ws/chat",
}

// ConnectRace opens every URL concurrently and returns the frame socket that
// connected first, closing the others the way the client closes them: code
// 1000 with the reason "loser socket". It mirrors
// openWebSocketsConcurrently (WAWebOpenSocket.js:44-64) — the first success
// wins, the outstanding dials are aborted, and the call fails only when every
// URL failed.
//
// Loser cleanup runs asynchronously. If all endpoints fail, errors are joined
// in endpoint order. The returned socket is connected and its read pump is
// running; the caller owns it as it owns one from NewFrameSocket plus Connect.
func ConnectRace(
	ctx context.Context,
	log waLog.Logger,
	httpClient *http.Client,
	urls []string,
	headers http.Header,
) (*FrameSocket, error) {
	fs, release, err := connectRace(ctx, log, httpClient, urls, headers, RaceOptions{}, nil)
	if release != nil {
		release()
	}
	return fs, err
}

// RaceOptions binds racing sockets to an owner before any of their workers
// start.
type RaceOptions struct {
	// Bind runs on every racing socket before it dials, so the owner can
	// install a worker launcher (see SetWorkerLauncher) and track the socket.
	// The race calls the returned release once it no longer needs a socket:
	// after a failed dial or a loser's close. The winner's release is returned
	// to the caller of ConnectRaceWithOptions.
	Bind func(*FrameSocket) (release func())
}

// ConnectRaceWithOptions is ConnectRace with owner binding. Every dial,
// read pump and loser close runs through the bound socket's worker launcher;
// when the launcher refuses, the race closes that socket inline instead. The
// caller must call the returned release (never nil on success) once it is
// done with the winner.
func ConnectRaceWithOptions(
	ctx context.Context,
	log waLog.Logger,
	httpClient *http.Client,
	urls []string,
	headers http.Header,
	opts RaceOptions,
) (*FrameSocket, func(), error) {
	return connectRace(ctx, log, httpClient, urls, headers, opts, nil)
}

type racer struct {
	fs      *FrameSocket
	release func()
}

type dialResult struct {
	index int
	r     racer
	err   error
}

func (r racer) done() {
	if r.release != nil {
		r.release()
	}
}

func connectRace(
	ctx context.Context,
	log waLog.Logger,
	httpClient *http.Client,
	urls []string,
	headers http.Header,
	opts RaceOptions,
	afterConnect func(int),
) (*FrameSocket, func(), error) {
	if len(urls) == 0 {
		return nil, nil, ErrDialFailed
	}
	newBound := func(url string) racer {
		r := racer{fs: newRacer(log, httpClient, url, headers)}
		if opts.Bind != nil {
			r.release = opts.Bind(r.fs)
		}
		return r
	}
	if len(urls) == 1 {
		r := newBound(urls[0])
		if err := r.fs.Connect(ctx); err != nil {
			r.fs.Close(0)
			r.done()
			return nil, nil, err
		}
		return r.fs, r.done, nil
	}

	results := make(chan dialResult, len(urls))
	cancels := make([]context.CancelFunc, len(urls))
	for i, url := range urls {
		dialCtx, cancel := context.WithCancel(ctx)
		cancels[i] = cancel
		r := newBound(url)
		dial := func() {
			err := r.fs.dial(ctx, dialCtx, false)
			cancel()
			if err == nil && afterConnect != nil {
				afterConnect(i)
			}
			results <- dialResult{i, r, err}
		}
		if !r.fs.startWorker(dial) {
			cancel()
			results <- dialResult{i, r, ErrSocketClosed}
		}
	}

	// Preserve endpoint order in the joined error so retry classification is
	// independent of the order in which unsuccessful dials finish.
	errs := make([]error, len(urls))
	for remaining := len(urls); remaining > 0; remaining-- {
		result := <-results
		if result.err != nil {
			errs[result.index] = result.err
			result.r.fs.Close(0)
			result.r.done()
			continue
		}
		for _, cancel := range cancels {
			cancel()
		}
		winner := result.r
		if !winner.fs.startReadPump() {
			// The owner sealed while this dial finished. Close the winner
			// and the losers inline rather than handing out a dead socket.
			drainLosers(results, remaining-1)
			winner.fs.Close(0)
			winner.done()
			return nil, nil, ErrSocketClosed
		}
		log.Debugf("Opened socket with %s (race winner)", winner.fs.URL)
		// Upgraded losers have no frame consumer. Never start their read pumps,
		// and don't delay the winner while waiting for their close handshakes.
		if !winner.fs.startWorker(func() { drainLosers(results, remaining-1) }) {
			drainLosers(results, remaining-1)
		}
		return winner.fs, winner.done, nil
	}
	return nil, nil, errors.Join(errs...)
}

// drainLosers waits for the outstanding dials and closes every loser that
// upgraded, through its own worker launcher when that is still admitted.
func drainLosers(results <-chan dialResult, count int) {
	for range count {
		result := <-results
		r, err := result.r, result.err
		closeLoser := func() {
			if err == nil {
				r.fs.CloseWithReason(websocket.StatusNormalClosure, LoserSocketCloseReason)
			} else {
				r.fs.Close(0)
			}
			r.done()
		}
		if err != nil || !r.fs.startWorker(closeLoser) {
			closeLoser()
		}
	}
}

func newRacer(log waLog.Logger, httpClient *http.Client, url string, headers http.Header) *FrameSocket {
	fs := NewFrameSocket(log, httpClient)
	fs.URL = url
	for name, values := range headers {
		fs.HTTPHeaders[name] = append([]string(nil), values...)
	}
	return fs
}
