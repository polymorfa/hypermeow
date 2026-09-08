//go:build !js

package socket

import (
	"context"
	"errors"
	"net/http"
	"sync"

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
// A client that makes one attempt per connect and never closes a second socket
// is distinguishable from the real one by nothing more than counting, which is
// why this is worth having.
//
// The returned socket is connected and its read pump is running; the caller
// owns it exactly as it owns one from NewFrameSocket plus Connect.
func ConnectRace(
	ctx context.Context,
	log waLog.Logger,
	httpClient *http.Client,
	urls []string,
	headers http.Header,
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

	// The decision is taken inside the racer goroutines under one lock, so a
	// socket that opens at the same instant as the winner is chosen either
	// becomes the winner or closes itself as the loser — there is no window
	// in which an already-open loser has its context cancelled from outside
	// and is torn down before it can send the close frame.
	var (
		mu     sync.Mutex
		winner *FrameSocket
		errs   []error
	)
	racerContexts := make([]context.Context, len(urls))
	cancels := make([]context.CancelFunc, len(urls))
	for i := range urls {
		racerContexts[i], cancels[i] = context.WithCancel(ctx)
	}
	var wg sync.WaitGroup
	for i, url := range urls {
		wg.Add(1)
		go func(i int, url string, racerCtx context.Context) {
			defer wg.Done()
			fs := newRacer(log, httpClient, url, headers)
			if err := fs.Connect(racerCtx); err != nil {
				fs.Close(0)
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			mu.Lock()
			if winner == nil {
				winner = fs
				mu.Unlock()
				log.Debugf("Opened socket with %s (race winner)", url)
				// Abort every dial still outstanding, exactly as the client
				// aborts its AbortController on the first success. The
				// winner's own context stays live: it is the parent of the
				// socket's context.
				for j, cancel := range cancels {
					if j != i {
						cancel()
					}
				}
				return
			}
			mu.Unlock()
			// A socket that opened after the winner is closed with the
			// client's own code and reason.
			fs.CloseWithReason(websocket.StatusNormalClosure, LoserSocketCloseReason)
		}(i, url, racerContexts[i])
	}
	wg.Wait()

	if winner == nil {
		for _, cancel := range cancels {
			cancel()
		}
		if len(errs) == 0 {
			return nil, ErrDialFailed
		}
		return nil, errors.Join(errs...)
	}
	return winner, nil
}

func newRacer(log waLog.Logger, httpClient *http.Client, url string, headers http.Header) *FrameSocket {
	fs := NewFrameSocket(log, httpClient)
	fs.URL = url
	for name, values := range headers {
		if len(values) == 0 {
			continue
		}
		fs.HTTPHeaders.Set(name, values[0])
	}
	return fs
}
