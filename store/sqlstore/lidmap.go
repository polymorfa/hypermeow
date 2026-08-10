// Copyright (c) 2025 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package sqlstore contains an SQL-backed implementation of the interfaces in the store package.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"go.mau.fi/util/exslices"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type CachedLIDMap struct {
	db *dbutil.Database

	pnToLIDCache map[string]string
	lidToPNCache map[string]string
	cacheFilled  bool
	lidCacheLock sync.RWMutex
}

var _ store.LIDStore = (*CachedLIDMap)(nil)
var _ store.LIDBatchReverseStore = (*CachedLIDMap)(nil)

const maxLIDCacheEntries = 65536

func NewCachedLIDMap(db *dbutil.Database) *CachedLIDMap {
	return &CachedLIDMap{
		db: db,

		pnToLIDCache: make(map[string]string),
		lidToPNCache: make(map[string]string),
	}
}

const (
	deleteExistingLIDMappingQuery = `DELETE FROM whatsmeow_lid_map WHERE (lid<>$1 AND pn=$2)`
	putLIDMappingQuery            = `
		INSERT INTO whatsmeow_lid_map (lid, pn)
		VALUES ($1, $2)
		ON CONFLICT (lid) DO UPDATE SET pn=excluded.pn WHERE whatsmeow_lid_map.pn<>excluded.pn
	`
	getLIDForPNQuery       = `SELECT lid FROM whatsmeow_lid_map WHERE pn=$1`
	getPNForLIDQuery       = `SELECT pn FROM whatsmeow_lid_map WHERE lid=$1`
	getAllLIDMappingsQuery = `SELECT lid, pn FROM whatsmeow_lid_map`
)

var convertLIDRow = dbutil.ConvertRowFn[store.LIDMapping](func(rows dbutil.Scannable) (store.LIDMapping, error) {
	var lidUser, pnUser string
	err := rows.Scan(&lidUser, &pnUser)
	if err != nil {
		return store.LIDMapping{}, err
	}
	return store.LIDMapping{
		LID: types.JID{User: lidUser, Server: types.DefaultUserServer},
		PN:  types.JID{User: pnUser, Server: types.DefaultUserServer},
	}, nil
})

func (s *CachedLIDMap) FillCache(ctx context.Context) error {
	s.lidCacheLock.Lock()
	defer s.lidCacheLock.Unlock()
	clear(s.pnToLIDCache)
	clear(s.lidToPNCache)
	res := convertLIDRow.NewRowIter(s.db.Query(ctx, getAllLIDMappingsQuery))
	count, err := s.scanManyLids(res, nil)
	s.cacheFilled = err == nil && count <= maxLIDCacheEntries
	return err
}

func (s *CachedLIDMap) scanManyLids(res dbutil.RowIter[store.LIDMapping], fn func(lid, pn string)) (int, error) {
	count := 0
	err := res.Iter(func(mapping store.LIDMapping) (bool, error) {
		count++
		s.cacheMappingLocked(mapping.LID.User, mapping.PN.User)
		if fn != nil {
			fn(mapping.LID.User, mapping.PN.User)
		}
		return true, nil
	})
	return count, err
}

func evictLIDCacheEntry(cache, reverse map[string]string) {
	for source, target := range cache {
		delete(cache, source)
		if target != "" && reverse[target] == source {
			delete(reverse, target)
		}
		return
	}
}

func (s *CachedLIDMap) cacheMissLocked(cache, reverse map[string]string, source string) {
	if _, exists := cache[source]; exists {
		return
	}
	if len(cache) >= maxLIDCacheEntries {
		evictLIDCacheEntry(cache, reverse)
	}
	cache[source] = ""
}

func (s *CachedLIDMap) cacheMappingLocked(lid, pn string) {
	if oldLID := s.pnToLIDCache[pn]; oldLID != "" && oldLID != lid && s.lidToPNCache[oldLID] == pn {
		delete(s.lidToPNCache, oldLID)
	}
	if oldPN := s.lidToPNCache[lid]; oldPN != "" && oldPN != pn && s.pnToLIDCache[oldPN] == lid {
		delete(s.pnToLIDCache, oldPN)
	}
	if _, exists := s.pnToLIDCache[pn]; !exists && len(s.pnToLIDCache) >= maxLIDCacheEntries {
		evictLIDCacheEntry(s.pnToLIDCache, s.lidToPNCache)
	}
	if _, exists := s.lidToPNCache[lid]; !exists && len(s.lidToPNCache) >= maxLIDCacheEntries {
		evictLIDCacheEntry(s.lidToPNCache, s.pnToLIDCache)
	}
	s.pnToLIDCache[pn] = lid
	s.lidToPNCache[lid] = pn
}

func (s *CachedLIDMap) getLIDMapping(ctx context.Context, source types.JID, targetServer, query string, sourceToTarget, targetToSource map[string]string) (types.JID, error) {
	s.lidCacheLock.RLock()
	targetUser, ok := sourceToTarget[source.User]
	cacheFilled := s.cacheFilled
	s.lidCacheLock.RUnlock()
	if ok || cacheFilled {
		if targetUser == "" {
			return types.JID{}, nil
		}
		return types.JID{User: targetUser, Device: source.Device, Server: targetServer}, nil
	}
	s.lidCacheLock.Lock()
	defer s.lidCacheLock.Unlock()
	err := s.db.QueryRow(ctx, query, source.User).Scan(&targetUser)
	if errors.Is(err, sql.ErrNoRows) {
		// continue with empty result
	} else if err != nil {
		return types.JID{}, err
	}
	if targetUser == "" {
		s.cacheMissLocked(sourceToTarget, targetToSource, source.User)
	}
	if targetUser != "" {
		if targetServer == types.HiddenUserServer {
			s.cacheMappingLocked(targetUser, source.User)
		} else {
			s.cacheMappingLocked(source.User, targetUser)
		}
		return types.JID{User: targetUser, Device: source.Device, Server: targetServer}, nil
	}
	return types.JID{}, nil
}

func (s *CachedLIDMap) GetLIDForPN(ctx context.Context, pn types.JID) (types.JID, error) {
	if pn.Server != types.DefaultUserServer {
		return types.JID{}, fmt.Errorf("invalid GetLIDForPN call with non-PN JID %s", pn)
	}
	return s.getLIDMapping(
		ctx, pn, types.HiddenUserServer, getLIDForPNQuery,
		s.pnToLIDCache, s.lidToPNCache,
	)
}

func (s *CachedLIDMap) GetPNForLID(ctx context.Context, lid types.JID) (types.JID, error) {
	if lid.Server != types.HiddenUserServer {
		return types.JID{}, fmt.Errorf("invalid GetPNForLID call with non-LID JID %s", lid)
	}
	return s.getLIDMapping(
		ctx, lid, types.DefaultUserServer, getPNForLIDQuery,
		s.lidToPNCache, s.pnToLIDCache,
	)
}

func (s *CachedLIDMap) GetManyLIDsForPNs(ctx context.Context, pns []types.JID) (map[types.JID]types.JID, error) {
	if len(pns) == 0 {
		return nil, nil
	}

	result := make(map[types.JID]types.JID, len(pns))

	s.lidCacheLock.RLock()
	missingPNs := make([]string, 0, len(pns))
	missingPNDevices := make(map[string][]types.JID)
	for _, pn := range pns {
		if pn.Server != types.DefaultUserServer {
			continue
		}
		if lidUser, ok := s.pnToLIDCache[pn.User]; ok && lidUser != "" {
			result[pn] = types.JID{User: lidUser, Device: pn.Device, Server: types.HiddenUserServer}
		} else if !s.cacheFilled {
			missingPNs = append(missingPNs, pn.User)
			missingPNDevices[pn.User] = append(missingPNDevices[pn.User], pn)
		}
	}
	s.lidCacheLock.RUnlock()

	if len(missingPNs) == 0 {
		return result, nil
	}

	s.lidCacheLock.Lock()
	defer s.lidCacheLock.Unlock()

	var res dbutil.RowIter[store.LIDMapping]
	if wrapped, ok := wrapPostgresArray(s.db, missingPNs); ok {
		res = convertLIDRow.NewRowIter(s.db.Query(
			ctx,
			`SELECT lid, pn FROM whatsmeow_lid_map WHERE pn = ANY($1)`,
			wrapped,
		))
	} else {
		placeholders := make([]string, len(missingPNs))
		for i := range missingPNs {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		res = convertLIDRow.NewRowIter(s.db.Query(
			ctx,
			fmt.Sprintf(`SELECT lid, pn FROM whatsmeow_lid_map WHERE pn IN (%s)`, strings.Join(placeholders, ",")),
			exslices.CastToAny(missingPNs)...,
		))
	}
	_, err := s.scanManyLids(res, func(lid, pn string) {
		for _, dev := range missingPNDevices[pn] {
			lidDev := dev
			lidDev.Server = types.HiddenUserServer
			lidDev.User = lid
			result[dev] = lidDev
		}
	})
	return result, err
}

func (s *CachedLIDMap) GetManyPNsForLIDs(ctx context.Context, lids []types.JID) (map[types.JID]types.JID, error) {
	if len(lids) == 0 {
		return nil, nil
	}

	result := make(map[types.JID]types.JID, len(lids))

	s.lidCacheLock.RLock()
	missingLIDs := make([]string, 0, len(lids))
	missingLIDDevices := make(map[string][]types.JID)
	for _, lid := range lids {
		if lid.Server != types.HiddenUserServer {
			continue
		}
		if pnUser, ok := s.lidToPNCache[lid.User]; ok {
			if pnUser != "" {
				result[lid] = types.JID{User: pnUser, Device: lid.Device, Server: types.DefaultUserServer}
			}
			continue
		}
		if !s.cacheFilled {
			if _, exists := missingLIDDevices[lid.User]; !exists {
				missingLIDs = append(missingLIDs, lid.User)
			}
			missingLIDDevices[lid.User] = append(missingLIDDevices[lid.User], lid)
		}
	}
	s.lidCacheLock.RUnlock()

	if len(missingLIDs) == 0 {
		return result, nil
	}

	s.lidCacheLock.Lock()
	defer s.lidCacheLock.Unlock()
	queryLIDs := missingLIDs[:0]
	for _, lid := range missingLIDs {
		if pn, ok := s.lidToPNCache[lid]; ok {
			if pn != "" {
				for _, dev := range missingLIDDevices[lid] {
					result[dev] = types.JID{User: pn, Device: dev.Device, Server: types.DefaultUserServer}
				}
			}
		} else if !s.cacheFilled {
			queryLIDs = append(queryLIDs, lid)
		}
	}
	missingLIDs = queryLIDs
	if len(missingLIDs) == 0 {
		return result, nil
	}

	found := make(map[string]struct{}, len(missingLIDs))
	scanResults := func(res dbutil.RowIter[store.LIDMapping]) error {
		_, err := s.scanManyLids(res, func(lid, pn string) {
			found[lid] = struct{}{}
			for _, dev := range missingLIDDevices[lid] {
				pnDev := dev
				pnDev.Server = types.DefaultUserServer
				pnDev.User = pn
				result[dev] = pnDev
			}
		})
		return err
	}
	if wrapped, ok := wrapPostgresArray(s.db, missingLIDs); ok {
		if err := scanResults(convertLIDRow.NewRowIter(s.db.Query(
			ctx,
			`SELECT lid, pn FROM whatsmeow_lid_map WHERE lid = ANY($1)`,
			wrapped,
		))); err != nil {
			return result, err
		}
	} else {
		for lids := range slices.Chunk(missingLIDs, lidQueryBatchSize) {
			placeholders := make([]string, len(lids))
			for i := range lids {
				placeholders[i] = fmt.Sprintf("$%d", i+1)
			}
			if err := scanResults(convertLIDRow.NewRowIter(s.db.Query(
				ctx,
				fmt.Sprintf(`SELECT lid, pn FROM whatsmeow_lid_map WHERE lid IN (%s)`, strings.Join(placeholders, ",")),
				exslices.CastToAny(lids)...,
			))); err != nil {
				return result, err
			}
		}
	}
	for _, lid := range missingLIDs {
		if _, ok := found[lid]; !ok {
			s.cacheMissLocked(s.lidToPNCache, s.pnToLIDCache, lid)
		}
	}
	return result, nil
}

const lidQueryBatchSize = 300

func (s *CachedLIDMap) PutLIDMapping(ctx context.Context, lid, pn types.JID) error {
	if lid.Server != types.HiddenUserServer || pn.Server != types.DefaultUserServer {
		return fmt.Errorf("invalid PutLIDMapping call %s/%s", lid, pn)
	}
	s.lidCacheLock.Lock()
	defer s.lidCacheLock.Unlock()
	cachedLID, ok := s.pnToLIDCache[pn.User]
	if ok && cachedLID == lid.User {
		return nil
	}
	return s.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		return s.unlockedPutLIDMapping(ctx, lid, pn)
	})
}

func (s *CachedLIDMap) PutManyLIDMappings(ctx context.Context, mappings []store.LIDMapping) error {
	s.lidCacheLock.Lock()
	defer s.lidCacheLock.Unlock()
	mappings = slices.DeleteFunc(mappings, func(mapping store.LIDMapping) bool {
		if mapping.LID.Server != types.HiddenUserServer || mapping.PN.Server != types.DefaultUserServer {
			zerolog.Ctx(ctx).Debug().
				Stringer("entry_lid", mapping.LID).
				Stringer("entry_pn", mapping.PN).
				Msg("Ignoring invalid entry in PutManyLIDMappings")
			return true
		}
		cachedLID, ok := s.pnToLIDCache[mapping.PN.User]
		if ok && cachedLID == mapping.LID.User {
			return true
		}
		return false
	})
	mappings = exslices.DeduplicateUnsortedOverwrite(mappings)
	if len(mappings) == 0 {
		return nil
	}
	return s.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		for _, mapping := range mappings {
			err := s.unlockedPutLIDMapping(ctx, mapping.LID, mapping.PN)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *CachedLIDMap) unlockedPutLIDMapping(ctx context.Context, lid, pn types.JID) error {
	if lid.Server != types.HiddenUserServer || pn.Server != types.DefaultUserServer {
		return fmt.Errorf("invalid PutLIDMapping call %s/%s", lid, pn)
	}
	_, err := s.db.Exec(ctx, deleteExistingLIDMappingQuery, lid.User, pn.User)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, putLIDMappingQuery, lid.User, pn.User)
	if err != nil {
		return err
	}
	s.cacheMappingLocked(lid.User, pn.User)
	return nil
}
