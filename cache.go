package whatsmeow

import "time"

const (
	maxGroupCacheEntries           = 8
	maxUserDeviceCacheEntries      = 2048
	maxMessageRetryEntries         = 1024
	maxIncomingRetryEntries        = 1024
	maxAppStateKeyRequestEntries   = 1024
	maxSessionRecreateHistoryItems = 1024
	retryCounterWindow             = time.Hour
)

func ensureMap[K comparable, V any](cache *map[K]V) map[K]V {
	if *cache == nil {
		*cache = make(map[K]V)
	}
	return *cache
}

func putBoundedCache[K comparable, V any](cache map[K]V, key K, value V, limit int) {
	if _, exists := cache[key]; !exists && len(cache) >= limit {
		for oldKey := range cache {
			delete(cache, oldKey)
			break
		}
	}
	cache[key] = value
}

func incrementBoundedCounter[K comparable](cache map[K]int, key K, limit int, resetAt *time.Time, now time.Time) (int, bool) {
	if resetAt.IsZero() || now.Sub(*resetAt) >= retryCounterWindow {
		clear(cache)
		*resetAt = now
	}
	if _, exists := cache[key]; !exists && len(cache) >= limit {
		return 0, false
	}
	cache[key]++
	return cache[key], true
}

func pruneExpiredCache[K comparable](cache map[K]time.Time, cutoff time.Time) {
	for key, timestamp := range cache {
		if timestamp.Before(cutoff) {
			delete(cache, key)
		}
	}
}
