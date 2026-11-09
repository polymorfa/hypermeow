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
	return connectRace(ctx, log, httpClient, urls, headers, nil)
}

func connectRace(
	ctx context.Context,
	log waLog.Logger,
	httpClient *http.Client,
	urls []string,
	headers http.Header,
	afterConnect func(int),
) (*FrameSocket, error) {
	if len(urls) == 0 {
		return nil, ErrDialFailed
	}
	if len(urls) == 1 {
		fs := newRacer(log, httpClient, urls[0], headers)
		if err := fs.Connect(ctx); err != nil {
			fs.Close(0)
			return nil, err
		}
		return fs, nil
	}

	type dialResult struct {
		index int
		fs    *FrameSocket
		err   error
	}
	results := make(chan dialResult, len(urls))
	cancels := make([]context.CancelFunc, len(urls))
	for i, url := range urls {
		dialCtx, cancel := context.WithCancel(ctx)
		cancels[i] = cancel
		go func() {
			fs := newRacer(log, httpClient, url, headers)
			err := fs.dial(ctx, dialCtx, false)
			cancel()
			if err == nil && afterConnect != nil {
				afterConnect(i)
			}
			results <- dialResult{i, fs, err}
		}()
	}

	// Preserve endpoint order in the joined error so retry classification is
	// independent of the order in which unsuccessful dials finish.
	errs := make([]error, len(urls))
	for remaining := len(urls); remaining > 0; remaining-- {
		result := <-results
		if result.err != nil {
			errs[result.index] = result.err
			continue
		}
		for _, cancel := range cancels {
			cancel()
		}
		result.fs.startReadPump()
		log.Debugf("Opened socket with %s (race winner)", result.fs.URL)
		// Upgraded losers have no frame consumer. Never start their read pumps,
		// and don't delay the winner while waiting for their close handshakes.
		go func() {
			for range remaining - 1 {
				loser := <-results
				if loser.err == nil {
					go loser.fs.CloseWithReason(websocket.StatusNormalClosure, LoserSocketCloseReason)
				}
			}
		}()
		return result.fs, nil
	}
	return nil, errors.Join(errs...)
}

func newRacer(log waLog.Logger, httpClient *http.Client, url string, headers http.Header) *FrameSocket {
	fs := NewFrameSocket(log, httpClient)
	fs.URL = url
	for name, values := range headers {
		fs.HTTPHeaders[name] = append([]string(nil), values...)
	}
	return fs
}
