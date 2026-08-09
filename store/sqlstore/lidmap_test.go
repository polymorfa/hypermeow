package sqlstore

import (
	"context"
	"fmt"
	"testing"

	"go.mau.fi/whatsmeow/types"
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

func TestGetManyPNsForLIDsUsesReverseCache(t *testing.T) {
	cache := NewCachedLIDMap(nil)
	cache.cacheFilled = true
	cache.cacheMappingLocked("100000000000001", "15550000001")
	cache.cacheMappingLocked("100000000000002", "15550000002")

	lids := []types.JID{
		types.NewJID("100000000000001", types.HiddenUserServer),
		types.NewJID("100000000000002", types.HiddenUserServer),
	}
	got, err := cache.GetManyPNsForLIDs(context.Background(), lids)
	if err != nil {
		t.Fatal(err)
	}
	for i, lid := range lids {
		want := fmt.Sprintf("1555000000%d@s.whatsapp.net", i+1)
		if got[lid].String() != want {
			t.Fatalf("phone for %s = %s, want %s", lid, got[lid], want)
		}
	}
}
