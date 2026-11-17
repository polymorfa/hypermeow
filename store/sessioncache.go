// Copyright (c) 2025 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/polymorfa/libsignal-protocol-go/state/record"
	"github.com/rs/zerolog"

	"go.mau.fi/util/exsync"
)

type contextKey int

const (
	contextKeySessionCache contextKey = iota
	contextKeySessionStateLock
)

// Bound completed reset history and outstanding deletion failures separately.
const maxSessionResetAddresses = 1024

var ErrSessionCacheInvalidated = errors.New("session cache was invalidated by an identity reset")

type sessionCacheEntry struct {
	Generation uint64
	Dirty      bool
	Found      bool
	Record     *record.Session
}

type sessionCache struct {
	*exsync.Map[string, sessionCacheEntry]
	epoch uint64
	owner *Device
}

type sessionStateGuard struct {
	device *Device
	parent *sessionStateGuard
}

// WithSessionStateLock orders local session mutations before database transactions.
// The callback context permits nested operations and must not escape the callback.
// Callers nesting different devices must use a consistent order across goroutines.
func (device *Device) WithSessionStateLock(ctx context.Context, fn func(context.Context) error) error {
	held, _ := ctx.Value(contextKeySessionStateLock).(*sessionStateGuard)
	for current := held; current != nil; current = current.parent {
		if current.device == device {
			return fn(ctx)
		}
	}
	device.sessionStateLock.Lock()
	defer device.sessionStateLock.Unlock()
	return fn(context.WithValue(ctx, contextKeySessionStateLock, &sessionStateGuard{device: device, parent: held}))
}

type sessionIterator interface {
	IterateSessions(context.Context, []string, func(string, []byte) error) error
}

type sessionLoader interface {
	IterateSession(context.Context, string, func([]byte) error) (bool, error)
}

func getSessionCache(ctx context.Context, device *Device) *sessionCache {
	if ctx == nil {
		return nil
	}
	val := ctx.Value(contextKeySessionCache)
	if val == nil {
		return nil
	}
	if cache, ok := val.(*sessionCache); ok && cache.owner == device {
		return cache
	}
	return nil
}

func getCachedSession(ctx context.Context, device *Device, addr string) *record.Session {
	cache := getSessionCache(ctx, device)
	if cache == nil {
		return nil
	}
	sess, ok := cache.Get(addr)
	if !ok {
		return nil
	}
	return sess.Record
}

func hasCachedSession(ctx context.Context, device *Device, addr string) (found, cached bool) {
	cache := getSessionCache(ctx, device)
	if cache == nil {
		return false, false
	}
	sess, cached := cache.Get(addr)
	return sess.Found, cached
}

func putCachedSession(ctx context.Context, device *Device, addr string, record *record.Session) bool {
	cache := getSessionCache(ctx, device)
	if cache == nil {
		return false
	}
	generation := cache.epoch
	if previous, ok := cache.Get(addr); ok {
		generation = previous.Generation
	}
	cache.Set(addr, sessionCacheEntry{
		Generation: generation,
		Dirty:      true,
		Found:      true,
		Record:     record,
	})
	return true
}

// CheckCachedSession rejects stale or incompletely reset state before local crypto.
func (device *Device) CheckCachedSession(ctx context.Context, address string) error {
	return device.WithSessionStateLock(ctx, func(ctx context.Context) error {
		return device.checkCachedSession(ctx, address)
	})
}

// The caller holds sessionStateLock across validation and local state use.
func (device *Device) checkCachedSession(ctx context.Context, address string) error {
	if _, pending := device.sessionResetPending[address]; pending {
		return ErrSessionCacheInvalidated
	}
	if cache := getSessionCache(ctx, device); cache != nil {
		generation := cache.epoch
		if item, ok := cache.Get(address); ok {
			generation = item.Generation
		}
		if generation < device.sessionResetFloor || device.sessionResetAddresses[address] > generation {
			return ErrSessionCacheInvalidated
		}
	}
	return nil
}

func (device *Device) WithCachedSessions(ctx context.Context, addresses []string) (map[string]bool, context.Context, error) {
	if len(addresses) == 0 {
		return nil, ctx, nil
	}
	var found map[string]bool
	var cached context.Context
	err := device.WithSessionStateLock(ctx, func(locked context.Context) (err error) {
		found, cached, err = device.withCachedSessions(locked, addresses)
		return
	})
	if err != nil {
		return nil, ctx, err
	}
	return found, context.WithValue(ctx, contextKeySessionCache, getSessionCache(cached, device)), nil
}

func (device *Device) withCachedSessions(ctx context.Context, addresses []string) (map[string]bool, context.Context, error) {
	if len(addresses) == 0 {
		return nil, ctx, nil
	}

	for _, address := range addresses {
		// A new prefetch must not bless a row whose deletion failed. The
		// previous context may be stale, so validate pending state alone.
		if _, pending := device.sessionResetPending[address]; pending {
			return nil, ctx, ErrSessionCacheInvalidated
		}
	}

	wrapped := make(map[string]sessionCacheEntry, len(addresses))
	existingSessions := make(map[string]bool, len(addresses))
	for _, addr := range addresses {
		wrapped[addr] = sessionCacheEntry{Generation: device.sessionResetEpoch, Record: record.NewSession(SignalProtobufSerializer.Session, SignalProtobufSerializer.State)}
		existingSessions[addr] = false
	}
	loadSession := func(addr string, rawSess []byte) error {
		sessionRecord, err := record.NewSessionFromBytes(rawSess, SignalProtobufSerializer.Session, SignalProtobufSerializer.State)
		if err != nil {
			zerolog.Ctx(ctx).Err(err).
				Str("address", addr).
				Msg("Failed to deserialize session")
			delete(wrapped, addr)
			delete(existingSessions, addr)
			return nil
		}
		existingSessions[addr] = true
		wrapped[addr] = sessionCacheEntry{Generation: device.sessionResetEpoch, Record: sessionRecord, Found: true}
		return nil
	}
	if iterator, ok := device.Sessions.(sessionIterator); ok {
		if err := iterator.IterateSessions(ctx, addresses, loadSession); err != nil {
			return nil, ctx, fmt.Errorf("failed to prefetch sessions: %w", err)
		}
	} else {
		sessions, err := device.Sessions.GetManySessions(ctx, addresses)
		if err != nil {
			return nil, ctx, fmt.Errorf("failed to prefetch sessions: %w", err)
		}
		for addr, rawSess := range sessions {
			if rawSess != nil {
				_ = loadSession(addr, rawSess)
			}
		}
	}

	ctx = context.WithValue(ctx, contextKeySessionCache, &sessionCache{Map: exsync.NewMapWithData(wrapped), epoch: device.sessionResetEpoch, owner: device})
	return existingSessions, ctx, nil
}

func (device *Device) PutCachedSessions(ctx context.Context) error {
	return device.WithSessionStateLock(ctx, device.putCachedSessions)
}

func (device *Device) putCachedSessions(ctx context.Context) error {
	cache := getSessionCache(ctx, device)
	if cache == nil {
		return nil
	}
	dirtySessions := make(map[string][]byte)
	invalid := false
	for addr, item := range cache.Iter() {
		if item.Dirty {
			if device.checkCachedSession(ctx, addr) != nil {
				invalid = true
				break
			}
			dirtySessions[addr] = item.Record.Serialize()
		}
	}
	if invalid {
		cache.Clear()
		return ErrSessionCacheInvalidated
	}
	if len(dirtySessions) > 0 {
		err := device.Sessions.PutManySessions(ctx, dirtySessions)
		if err != nil {
			return fmt.Errorf("failed to store cached sessions: %w", err)
		}
	}
	cache.Clear()
	return nil
}
