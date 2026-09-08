// Copyright (c) 2025 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package socket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"

	waLog "github.com/polymorfa/hypermeow/util/log"
)

type FrameSocket struct {
	parentCtx context.Context
	cancelCtx context.Context
	cancel    context.CancelFunc
	conn      atomic.Pointer[websocket.Conn]
	log       waLog.Logger
	lock      sync.Mutex

	URL         string
	HTTPHeaders http.Header
	HTTPClient  *http.Client

	Frames       chan []byte
	OnDisconnect func(ctx context.Context, remote bool)

	Header []byte

	closed atomic.Bool

	incomingLength int
	receivedLength int
	incoming       []byte
	partialHeader  []byte
}

func NewFrameSocket(log waLog.Logger, client *http.Client) *FrameSocket {
	return &FrameSocket{
		log:    log,
		Header: WAConnHeader,
		Frames: make(chan []byte),

		URL: URL,
		HTTPHeaders: http.Header{
			"Origin":        {Origin},
			"Cache-Control": {"no-cache"},
			"Pragma":        {"no-cache"},
		},
		HTTPClient: client,
	}
}

func (fs *FrameSocket) IsConnected() bool {
	return fs.conn.Load() != nil
}

func (fs *FrameSocket) Close(code websocket.StatusCode) {
	fs.CloseWithReason(code, "")
}

// CloseWithReason closes the socket with an explicit close reason.
//
// WhatsApp Web races two websockets on every connect and closes the one that
// loses with code 1000 and the reason "loser socket"
// (WAWebOpenSocket.js:44-52). A close with an empty reason where the client
// sends one is observable, so the reason is part of the wire behaviour and not
// a log detail.
func (fs *FrameSocket) CloseWithReason(code websocket.StatusCode, reason string) {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	conn := fs.conn.Swap(nil)
	if conn == nil {
		return
	}

	fs.closed.Store(true)
	if code > 0 {
		err := conn.Close(code, reason)
		if err != nil {
			fs.log.Warnf("Error sending close to websocket: %v", err)
		}
	} else {
		err := conn.CloseNow()
		if err != nil {
			fs.log.Debugf("Error force closing websocket: %v", err)
		}
	}
	fs.cancel()
	fs.cancel = nil
	if fs.OnDisconnect != nil {
		go fs.OnDisconnect(fs.parentCtx, code == 0)
	}
}

func (fs *FrameSocket) Connect(ctx context.Context) error {
	return fs.connect(ctx, ctx)
}

// connect separates the lifetime of the opened socket from the context used
// for the HTTP upgrade. Most callers use the same context for both through
// Connect. Socket racing uses a short-lived dial context so aborting another
// in-flight upgrade cannot cancel a connection that has already opened.
func (fs *FrameSocket) connect(parentCtx, dialCtx context.Context) error {
	fs.lock.Lock()
	defer fs.lock.Unlock()
	if fs.conn.Load() != nil {
		return ErrSocketAlreadyOpen
	}
	fs.parentCtx = parentCtx
	fs.cancelCtx, fs.cancel = context.WithCancel(parentCtx)

	fs.log.Debugf("Dialing %s", fs.URL)
	conn, resp, err := websocket.Dial(dialCtx, fs.URL, fs.makeDialOptions())
	if err != nil {
		if resp != nil {
			err = ErrWithStatusCode{err, resp.StatusCode}
		}
		fs.cancel()
		return fmt.Errorf("%w: %w", ErrDialFailed, err)
	}
	conn.SetReadLimit(FrameMaxSize)

	fs.conn.Store(conn)

	go fs.readPump(conn, fs.cancelCtx)
	return nil
}

func (fs *FrameSocket) Context() context.Context {
	return fs.cancelCtx
}

func (fs *FrameSocket) SendFrame(data []byte) error {
	conn := fs.conn.Load()
	if conn == nil {
		return ErrSocketClosed
	}
	dataLength := len(data)
	if dataLength >= FrameMaxSize {
		return fmt.Errorf("%w (got %d bytes, max %d bytes)", ErrFrameTooLarge, len(data), FrameMaxSize)
	}

	headerLength := len(fs.Header)
	// Whole frame is header + 3 bytes for length + data
	wholeFrame := make([]byte, headerLength+FrameLengthSize+dataLength)

	// Copy the header if it's there
	if fs.Header != nil {
		copy(wholeFrame[:headerLength], fs.Header)
		// We only want to send the header once
		fs.Header = nil
	}

	// Encode length of frame
	wholeFrame[headerLength] = byte(dataLength >> 16)
	wholeFrame[headerLength+1] = byte(dataLength >> 8)
	wholeFrame[headerLength+2] = byte(dataLength)

	// Copy actual frame data
	copy(wholeFrame[headerLength+FrameLengthSize:], data)

	return conn.Write(fs.cancelCtx, websocket.MessageBinary, wholeFrame)
}

func (fs *FrameSocket) frameComplete() {
	data := fs.incoming
	fs.incoming = nil
	fs.partialHeader = nil
	fs.incomingLength = 0
	fs.receivedLength = 0
	fs.Frames <- data
}

func (fs *FrameSocket) processData(msg []byte) {
	for len(msg) > 0 {
		// This probably doesn't happen a lot (if at all), so the code is unoptimized
		if fs.partialHeader != nil {
			msg = append(fs.partialHeader, msg...)
			fs.partialHeader = nil
		}
		if fs.incoming == nil {
			if len(msg) >= FrameLengthSize {
				length := (int(msg[0]) << 16) + (int(msg[1]) << 8) + int(msg[2])
				fs.incomingLength = length
				fs.receivedLength = len(msg)
				msg = msg[FrameLengthSize:]
				if len(msg) >= length {
					fs.incoming = msg[:length]
					msg = msg[length:]
					fs.frameComplete()
				} else {
					fs.incoming = make([]byte, length)
					copy(fs.incoming, msg)
					msg = nil
				}
			} else {
				fs.log.Warnf("Received partial header (report if this happens often)")
				fs.partialHeader = msg
				msg = nil
			}
		} else {
			if fs.receivedLength+len(msg) >= fs.incomingLength {
				copy(fs.incoming[fs.receivedLength:], msg[:fs.incomingLength-fs.receivedLength])
				msg = msg[fs.incomingLength-fs.receivedLength:]
				fs.frameComplete()
			} else {
				copy(fs.incoming[fs.receivedLength:], msg)
				fs.receivedLength += len(msg)
				msg = nil
			}
		}
	}
}

func (fs *FrameSocket) readPump(conn *websocket.Conn, ctx context.Context) {
	fs.log.Debugf("Frame websocket read pump starting %p", fs)
	defer func() {
		fs.log.Debugf("Frame websocket read pump exiting %p", fs)
		go fs.Close(0)
	}()
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			// Ignore the error if the context has been closed
			if !fs.closed.Load() && !errors.Is(ctx.Err(), context.Canceled) {
				fs.log.Errorf("Error reading from websocket: %v", err)
			}
			return
		} else if msgType != websocket.MessageBinary {
			fs.log.Warnf("Got unexpected websocket message type %d", msgType)
			continue
		}
		fs.processData(data)
	}
}
