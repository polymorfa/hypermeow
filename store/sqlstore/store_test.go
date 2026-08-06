package sqlstore

import (
	"fmt"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestNewSQLStoreDefersCaches(t *testing.T) {
	store := NewSQLStore(nil, types.JID{User: "123", Server: types.DefaultUserServer})
	if store.contactCache != nil || store.identityCache != nil || store.migratedPNSessionsCache != nil || store.migratingPNSessions != nil {
		t.Fatal("SQL store allocated caches before use")
	}
}

func TestBuildSharedMassInsertQuery(t *testing.T) {
	query := buildSharedMassInsertQuery("INSERT VALUES ", " ON CONFLICT", 2, 2)
	want := "INSERT VALUES ($1,$2,$3),($1,$4,$5) ON CONFLICT"
	if query != want {
		t.Fatalf("unexpected query:\n%s\nwant:\n%s", query, want)
	}
}

func TestIdentityCacheIsBounded(t *testing.T) {
	store := &SQLStore{identityCache: make(map[string]identityCacheEntry, maxIdentityCacheEntries)}
	for i := 0; i < maxIdentityCacheEntries; i++ {
		store.setCachedIdentityLocked(fmt.Sprintf("%d:1", i), identityCacheEntry{Present: true})
	}
	store.setCachedIdentityLocked("new:1", identityCacheEntry{Present: true})

	if len(store.identityCache) != maxIdentityCacheEntries {
		t.Fatalf("cache grew to %d entries", len(store.identityCache))
	}
	if _, ok := store.identityCache["new:1"]; !ok {
		t.Fatal("new identity was not cached")
	}
}

func TestContactCacheIsBounded(t *testing.T) {
	store := &SQLStore{contactCache: make(map[types.JID]*types.ContactInfo, maxContactCacheEntries)}
	for i := 0; i < maxContactCacheEntries; i++ {
		jid := types.NewJID(fmt.Sprintf("%d", i), types.DefaultUserServer)
		store.setCachedContactLocked(jid, &types.ContactInfo{Found: true})
	}
	newJID := types.NewJID("new", types.DefaultUserServer)
	store.setCachedContactLocked(newJID, &types.ContactInfo{Found: true})

	if len(store.contactCache) != maxContactCacheEntries {
		t.Fatalf("cache grew to %d entries", len(store.contactCache))
	}
	if _, ok := store.contactCache[newJID]; !ok {
		t.Fatal("new contact was not cached")
	}
}
