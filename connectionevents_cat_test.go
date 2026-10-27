// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"errors"
	"testing"
	"time"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types/events"
)

func TestCATFailuresRequireSuccessfulRefreshBeforeReconnect(t *testing.T) {
	refreshErr := errors.New("synthetic CAT refresh failure")
	for _, reason := range []events.ConnectFailureReason{events.ConnectFailureCATExpired, events.ConnectFailureCATInvalid} {
		for _, source := range []string{"connect", "stream"} {
			for _, outcome := range []string{"unconfigured", "failed", "refreshed"} {
				t.Run(source+"/"+reason.NumberString()+"/"+outcome, func(t *testing.T) {
					cli, _, _ := newTestRemote(t)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					failures := make(chan *events.CATRefreshError, 1)
					scopedFailures := make(chan context.Context, 1)
					cli.AddConnectionEventHandler(func(source context.Context, evt any) {
						if _, ok := evt.(*events.CATRefreshError); ok {
							scopedFailures <- source
						}
					})
					cli.AddEventHandler(func(evt any) {
						if failure, ok := evt.(*events.CATRefreshError); ok {
							failures <- failure
						}
					})
					calls := 0
					if outcome != "unconfigured" {
						cli.RefreshCAT = func(got context.Context) error {
							calls++
							if got != ctx {
								t.Error("CAT refresh lost its connection context")
							}
							if outcome == "failed" {
								return refreshErr
							}
							return nil
						}
					}
					cli.forceAutoReconnect.Store(true)
					if source == "connect" {
						cli.handleConnectFailure(ctx, &waBinary.Node{Tag: "failure", Attrs: waBinary.Attrs{"reason": reason.NumberString()}})
					} else {
						cli.handleStreamError(ctx, &waBinary.Node{Tag: "stream:error", Attrs: waBinary.Attrs{"code": reason.NumberString()}})
					}
					if outcome == "refreshed" {
						if calls != 1 || cli.isExpectedDisconnect() {
							t.Fatal("successful refresh did not leave reconnect available")
						}
						select {
						case <-failures:
							t.Fatal("successful refresh emitted a failure")
						default:
						}
						return
					}
					if !cli.isExpectedDisconnect() || cli.forceAutoReconnect.Load() {
						t.Fatal("CAT failure left automatic reconnect enabled")
					}
					if outcome == "failed" && calls != 1 || outcome == "unconfigured" && calls != 0 {
						t.Fatalf("unexpected CAT refresh calls: %d", calls)
					}
					select {
					case failure := <-failures:
						if failure.Error == nil || outcome == "failed" && !errors.Is(failure.Error, refreshErr) {
							t.Fatalf("CAT failure lost its error: %v", failure.Error)
						}
					case <-time.After(time.Second):
						t.Fatal("CAT failure did not emit its permanent-disconnect event")
					}
					select {
					case source := <-scopedFailures:
						if source != ctx {
							t.Fatal("CAT failure lost its original connection context")
						}
					default:
						t.Fatal("CAT failure did not reach connection lifecycle observers")
					}
				})
			}
		}
	}
}
