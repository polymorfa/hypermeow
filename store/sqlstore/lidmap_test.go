package sqlstore

import (
	"fmt"
	"testing"
)

func TestLIDCacheIsBounded(t *testing.T) {
	cache := NewCachedLIDMap(nil)
	for i := 0; i <= maxLIDCacheEntries; i++ {
		cache.cacheMappingLocked(fmt.Sprintf("lid-%d", i), fmt.Sprintf("pn-%d", i))
	}

	if len(cache.pnToLIDCache) > maxLIDCacheEntries {
		t.Fatalf("PN cache grew to %d entries", len(cache.pnToLIDCache))
	}
	if len(cache.lidToPNCache) > maxLIDCacheEntries {
		t.Fatalf("LID cache grew to %d entries", len(cache.lidToPNCache))
	}
	if cache.pnToLIDCache[fmt.Sprintf("pn-%d", maxLIDCacheEntries)] != fmt.Sprintf("lid-%d", maxLIDCacheEntries) {
		t.Fatal("new mapping was not cached")
	}
	for pn, lid := range cache.pnToLIDCache {
		if lid != "" && cache.lidToPNCache[lid] != pn {
			t.Fatalf("inconsistent cache mapping %s -> %s", pn, lid)
		}
	}
}
