// Copyright (c) 2022 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package whatsmeow

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polymorfa/hypermeow/socket"
	"github.com/polymorfa/hypermeow/types/events"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

type QRChannelItem struct {
	// The type of event, "code" for new QR codes (see Code field) and "error" for pairing errors (see Error) field.
	// For non-code/error events, you can just compare the whole item to the event variables (like QRChannelSuccess).
	Event string
	// If the item is a pair error, then this field contains the error message.
	Error error
	// If the item is a new code, then this field contains the raw data.
	Code string
	// The timeout after which the next code will be sent down the channel.
	Timeout time.Duration

	PasskeyRequest      *events.PairPasskeyRequest
	PasskeyConfirmation *events.PairPasskeyConfirmation
}

const QRChannelEventCode = "code"
const QRChannelEventError = "error"
const QRChannelEventPasskeyRequest = "passkey-request"
const QRChannelEventPasskeyResponse = "passkey-confirmation"

// Possible final items in the QR channel. In addition to these, an `error` event may be emitted,
// in which case the Error field will have the error that occurred during pairing.
var (
	// QRChannelSuccess is emitted from GetQRChannel when the pairing is successful.
	QRChannelSuccess = QRChannelItem{Event: "success"}
	// QRChannelTimeout is emitted from GetQRChannel if the socket gets disconnected by the server before the pairing is successful.
	QRChannelTimeout = QRChannelItem{Event: "timeout"}
	// QRChannelErrUnexpectedEvent is emitted from GetQRChannel if an unexpected connection event is received,
	// as that likely means that the pairing has already happened before the channel was set up.
	QRChannelErrUnexpectedEvent = QRChannelItem{Event: "err-unexpected-state"}
	// QRChannelClientOutdated is emitted from GetQRChannel if events.ClientOutdated is received.
	QRChannelClientOutdated = QRChannelItem{Event: "err-client-outdated"}
	// QRChannelScannedWithoutMultidevice is emitted from GetQRChannel if events.QRScannedWithoutMultidevice is received.
	QRChannelScannedWithoutMultidevice = QRChannelItem{Event: "err-scanned-without-multidevice"}
)

type qrChannel struct {
	sync.Mutex
	cli          *Client
	log          waLog.Logger
	ctx          context.Context
	handlerID    uint32
	closed       atomic.Bool
	output       chan<- QRChannelItem
	stopQRs      chan struct{}
	sendsDone    chan struct{}
	outputClosed chan struct{}
	senders      int
	terminal     *QRChannelItem
	disconnect   bool
	boundSocket  *socket.NoiseSocket
}

// requestClose seals sends once. Only the reserved cleanup worker closes output
// or removes the handler. It never waits while holding QR/client state locks.
func (qrc *qrChannel) requestClose(item *QRChannelItem, disconnect bool, expected *socket.NoiseSocket) bool {
	qrc.Lock()
	defer qrc.Unlock()
	if qrc.closed.Load() {
		return false
	}
	qrc.terminal, qrc.disconnect, qrc.boundSocket = item, disconnect, expected
	qrc.closed.Store(true)
	close(qrc.stopQRs)
	if qrc.senders == 0 {
		close(qrc.sendsDone)
	}
	return true
}

func (qrc *qrChannel) send(item QRChannelItem, nonblocking bool) bool {
	qrc.Lock()
	if qrc.closed.Load() {
		qrc.Unlock()
		return false
	}
	qrc.senders++
	qrc.Unlock()
	defer func() {
		qrc.Lock()
		qrc.senders--
		if qrc.closed.Load() && qrc.senders == 0 {
			close(qrc.sendsDone)
		}
		qrc.Unlock()
	}()
	if nonblocking {
		select {
		case qrc.output <- item:
			return true
		case <-qrc.stopQRs:
			return false
		case <-qrc.ctx.Done():
			return false
		default:
			return false
		}
	}
	select {
	case qrc.output <- item:
		return true
	case <-qrc.stopQRs:
		return false
	case <-qrc.ctx.Done():
		return false
	}
}

func (qrc *qrChannel) run() {
	select {
	case <-qrc.stopQRs:
	case <-qrc.ctx.Done():
		qrc.Lock()
		expected := qrc.boundSocket
		qrc.Unlock()
		qrc.requestClose(nil, expected != nil, expected)
	}
	<-qrc.sendsDone
	qrc.Lock()
	terminal, disconnect, expected := qrc.terminal, qrc.disconnect, qrc.boundSocket
	qrc.Unlock()
	if disconnect && !qrc.cli.nativeClosed() {
		qrc.cli.disconnectQRSocket(expected)
	}
	if terminal != nil && qrc.ctx.Err() == nil {
		select {
		case qrc.output <- *terminal:
		case <-qrc.ctx.Done():
		}
	}
	close(qrc.output)
	// Wake terminal callbacks before acquiring eventHandlersLock for removal.
	// They are running under its read lock and must be allowed to return first.
	close(qrc.outputClosed)
	qrc.cli.RemoveEventHandler(qrc.handlerID)
}

func (qrc *qrChannel) emitQRs(codes []string, expected *socket.NoiseSocket) {
	for {
		if len(codes) == 0 {
			qrc.requestClose(&QRChannelTimeout, true, expected)
			return
		} else if qrc.closed.Load() {
			return
		}
		timeout := 20 * time.Second
		if len(codes) == 6 {
			timeout = 60 * time.Second
		}
		nextCode := codes[0]
		codes = codes[1:]
		qrc.log.Debugf("Emitting QR code %s", nextCode)
		if !qrc.send(QRChannelItem{Code: nextCode, Timeout: timeout, Event: QRChannelEventCode}, true) {
			qrc.log.Debugf("Output channel didn't accept code, exiting QR emitter")
			qrc.requestClose(nil, true, expected)
			return
		}
		timer := time.NewTimer(timeout)
		select {
		case <-timer.C:
		case <-qrc.stopQRs:
			timer.Stop()
			return
		case <-qrc.cli.expectedDisconnect.GetChan():
			timer.Stop()
			return
		case <-qrc.ctx.Done():
			timer.Stop()
			qrc.requestClose(nil, true, expected)
			return
		}
	}
}

func (qrc *qrChannel) handleEvent(rawEvt any) {
	if qrc.closed.Load() {
		qrc.log.Debugf("Dropping event of type %T, channel is closed", rawEvt)
		return
	}
	var outputType QRChannelItem
	switch evt := rawEvt.(type) {
	case *events.QR:
		qrc.log.Debugf("Received QR code event, starting to emit codes to channel")
		codes := slices.Clone(evt.Codes)
		qrc.cli.socketLock.RLock()
		expected := qrc.cli.socket
		qrc.cli.socketLock.RUnlock()
		qrc.Lock()
		if !qrc.closed.Load() {
			qrc.boundSocket = expected
			// QR-local closure and emitter admission share this lock. The
			// helper only reserves/schedules; it never waits for worker exit.
			qrc.cli.startNativeContext(qrc.ctx, func() { qrc.emitQRs(codes, expected) })
		}
		qrc.Unlock()
		return
	case *events.QRScannedWithoutMultidevice:
		qrc.log.Debugf("QR code scanned without multidevice enabled")
		qrc.send(QRChannelScannedWithoutMultidevice, false)
		return
	case *events.PairPasskeyRequest:
		qrc.send(QRChannelItem{
			Event:          QRChannelEventPasskeyRequest,
			PasskeyRequest: evt,
		}, false)
		return
	case *events.PairPasskeyConfirmation:
		if evt.SkipHandoffUX {
			qrc.log.Debugf("Sending automatic passkey confirmation")
			err := qrc.cli.SendPasskeyConfirmation(qrc.ctx)
			if err != nil {
				qrc.send(QRChannelItem{
					Event: QRChannelEventError,
					Error: fmt.Errorf("failed to send passkey confirmation automatically: %w", err),
				}, false)
			}
		} else {
			qrc.send(QRChannelItem{
				Event:               QRChannelEventPasskeyResponse,
				PasskeyConfirmation: evt,
			}, false)
		}
		return
	case *events.PairPasskeyError:
		qrc.send(QRChannelItem{
			Event: QRChannelEventError,
			Error: evt.Error,
		}, false)
		return
	case *events.ClientOutdated:
		outputType = QRChannelClientOutdated
	case *events.PairSuccess:
		outputType = QRChannelSuccess
	case *events.PairError:
		outputType = QRChannelItem{
			Event: QRChannelEventError,
			Error: evt.Error,
		}
	case *events.Disconnected:
		outputType = QRChannelTimeout
	case *events.Connected, *events.ConnectFailure, *events.LoggedOut, *events.TemporaryBan:
		outputType = QRChannelErrUnexpectedEvent
	default:
		return
	}
	qrc.requestClose(&outputType, false, nil)
	select {
	case <-qrc.outputClosed:
	case <-qrc.ctx.Done():
	}
}

// QR cancellation/expiry belongs to the socket observed by that QR emitter,
// not a later socket on the same Client. No socket identity means no disconnect
// authority. Cleanup of pending requests stays inside this exact-pointer guard.
func (cli *Client) disconnectQRSocket(expected *socket.NoiseSocket) {
	if expected == nil {
		return
	}
	cli.socketLock.Lock()
	defer cli.socketLock.Unlock()
	if cli.socket != expected {
		return
	}
	cli.expectDisconnect()
	cli.unlockedDisconnect()
	cli.clearDelayedMessageRequests()
}

// GetQRChannel returns a channel that automatically outputs a new QR code when the previous one expires.
//
// This must be called *before* Connect(). It will then listen to all the relevant events from the client.
//
// Pairing terminal events emit a special item such as "success", "timeout" or
// an error, then close the channel. Caller cancellation, final client close or
// output backpressure can close it without a terminal pairing item. Output close
// alone does not confirm successful pairing or complete client termination.
func (cli *Client) GetQRChannel(ctx context.Context) (<-chan QRChannelItem, error) {
	if cli == nil {
		return nil, ErrClientIsNil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	done, admitted := cli.enterNative()
	if !admitted {
		return nil, ErrClientClosed
	}
	launched := false
	defer func() {
		if !launched {
			done()
		}
	}()
	if cli.IsConnected() {
		return nil, ErrQRAlreadyConnected
	}
	if cli.Store.ID != nil {
		return nil, ErrQRStoreContainsID
	}
	ctx, release := cli.nativeContext(ctx)
	defer func() {
		if !launched {
			release()
		}
	}()
	ch := make(chan QRChannelItem, 8)
	qrc := &qrChannel{
		output: ch, stopQRs: make(chan struct{}), sendsDone: make(chan struct{}), outputClosed: make(chan struct{}),
		cli: cli, log: cli.Log.Sub("QRChannel"), ctx: ctx,
	}
	qrc.handlerID = cli.AddEventHandler(qrc.handleEvent)
	// This owner was recorded before handler registration, not re-admitted after
	// it. Even a final seal during registration must join its exact cleanup.
	launched = true
	go func() { defer done(); defer release(); qrc.run() }()
	if cli.nativeClosed() {
		return nil, ErrClientClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ch, nil
}
