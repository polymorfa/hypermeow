package sqlstore

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

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

func TestMigratePNToLIDSkipsTransactionWhenNoPNRowsExist(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLStore(
		NewWithDB(db, "postgres", nil),
		types.NewJID("15550000000", types.DefaultUserServer),
	)
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT EXISTS(SELECT 1 FROM whatsmeow_sessions WHERE our_jid=$1 AND their_id>=$2 AND their_id<$3)
			OR EXISTS(SELECT 1 FROM whatsmeow_identity_keys WHERE our_jid=$1 AND their_id>=$2 AND their_id<$3)
			OR EXISTS(SELECT 1 FROM whatsmeow_sender_keys WHERE our_jid=$1 AND sender_id>=$2 AND sender_id<$3)
	`)).
		WithArgs(store.JID, "15551234567:", "15551234567;").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	if err = store.MigratePNToLID(context.Background(), pn, lid); err != nil {
		t.Fatal(err)
	}
	if err = store.MigratePNToLID(context.Background(), pn, lid); err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
