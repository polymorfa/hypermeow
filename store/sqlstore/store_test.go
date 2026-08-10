package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type pnMigrationTestDB struct {
	queries int
	begins  int
	query   string
	args    []driver.NamedValue
}

type pnMigrationTestConnector struct{ state *pnMigrationTestDB }

func (c *pnMigrationTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &pnMigrationTestConn{state: c.state}, nil
}

func (*pnMigrationTestConnector) Driver() driver.Driver { return pnMigrationTestDriver{} }

type pnMigrationTestDriver struct{}

func (pnMigrationTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type pnMigrationTestConn struct{ state *pnMigrationTestDB }

func (*pnMigrationTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (*pnMigrationTestConn) Close() error { return nil }

func (c *pnMigrationTestConn) Begin() (driver.Tx, error) {
	c.state.begins++
	return nil, errors.New("unexpected transaction")
}

func (c *pnMigrationTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.state.queries++
	c.state.query = query
	c.state.args = append([]driver.NamedValue(nil), args...)
	return &pnMigrationTestRows{}, nil
}

type pnMigrationTestRows struct{ read bool }

func (*pnMigrationTestRows) Columns() []string { return []string{"exists"} }
func (*pnMigrationTestRows) Close() error      { return nil }
func (r *pnMigrationTestRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = false
	return nil
}

type contactUsernameRaceDB struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

type contactUsernameRaceConnector struct{ state *contactUsernameRaceDB }

func (connector *contactUsernameRaceConnector) Connect(context.Context) (driver.Conn, error) {
	return &contactUsernameRaceConn{state: connector.state}, nil
}

func (*contactUsernameRaceConnector) Driver() driver.Driver { return pnMigrationTestDriver{} }

type contactUsernameRaceConn struct{ state *contactUsernameRaceDB }

func (*contactUsernameRaceConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (*contactUsernameRaceConn) Close() error { return nil }
func (*contactUsernameRaceConn) Begin() (driver.Tx, error) {
	return contactUsernameRaceTx{}, nil
}

func (conn *contactUsernameRaceConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	conn.state.once.Do(func() {
		close(conn.state.entered)
		<-conn.state.release
	})
	return driver.RowsAffected(1), nil
}

type contactUsernameRaceTx struct{}

func (contactUsernameRaceTx) Commit() error   { return nil }
func (contactUsernameRaceTx) Rollback() error { return nil }

func TestNewSQLStoreDefersCaches(t *testing.T) {
	store := NewSQLStore(nil, types.JID{User: "123", Server: types.DefaultUserServer})
	if store.contactCache != nil || store.identityCache != nil || store.migratedPNSessionsCache != nil || store.emptyPNMigrationCache != nil || store.migratingPNSessions != nil {
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

func TestBulkContactNamesPreserveMissingUsername(t *testing.T) {
	withUsername, withoutUsername := splitContactNameEntries([]store.ContactEntry{
		{JID: types.NewJID("100", types.HiddenUserServer)},
		{JID: types.NewJID("101", types.HiddenUserServer), Username: "named"},
		{JID: types.NewJID("102", types.HiddenUserServer), UsernameSet: true},
	})
	if len(withoutUsername) != 1 || withoutUsername[0].JID.User != "100" {
		t.Fatalf("name-only contacts = %#v", withoutUsername)
	}
	if len(withUsername) != 2 || withUsername[0].Username != "named" || !withUsername[1].UsernameSet || withUsername[1].Username != "" {
		t.Fatalf("username-aware contacts = %#v", withUsername)
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

func TestPutManyContactUsernamesSerializesSingleWrites(t *testing.T) {
	state := &contactUsernameRaceDB{entered: make(chan struct{}), release: make(chan struct{})}
	db := sql.OpenDB(&contactUsernameRaceConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	jid := types.NewJID("100000011111111", types.HiddenUserServer)
	sqlStore := NewSQLStore(NewWithDB(db, "postgres", nil), types.NewJID("15550000000", types.DefaultUserServer))
	sqlStore.contactCache = map[types.JID]*types.ContactInfo{jid: {Found: true, Username: "initial"}}

	batchDone := make(chan error, 1)
	go func() {
		batchDone <- sqlStore.PutManyContactUsernames(context.Background(), []store.ContactUsernameEntry{{JID: jid, Username: "batch"}})
	}()
	<-state.entered
	singleDone := make(chan error, 1)
	go func() {
		singleDone <- sqlStore.PutContactUsername(context.Background(), jid, "single")
	}()
	select {
	case err := <-singleDone:
		t.Fatalf("single write overtook batch write: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(state.release)
	if err := <-batchDone; err != nil {
		t.Fatal(err)
	}
	if err := <-singleDone; err != nil {
		t.Fatal(err)
	}
	if got := sqlStore.contactCache[jid].Username; got != "single" {
		t.Fatalf("cached username = %q, want single", got)
	}
}

func TestDeduplicateContactUsernamesUsesLastAlias(t *testing.T) {
	firstJID := types.NewJID("100000011111111", types.HiddenUserServer)
	secondJID := types.NewJID("100000022222222", types.HiddenUserServer)
	entries := deduplicateContactUsernamesLastWriteWins([]store.ContactUsernameEntry{
		{JID: firstJID, Username: "old"},
		{JID: secondJID, Username: "other"},
		{JID: firstJID, Username: "new"},
	})
	if len(entries) != 2 {
		t.Fatalf("deduplicated entries = %#v", entries)
	}
	if entries[0].JID != firstJID || entries[0].Username != "new" {
		t.Fatalf("first entry = %#v, want latest alias for %s", entries[0], firstJID)
	}
	if entries[1].JID != secondJID || entries[1].Username != "other" {
		t.Fatalf("second entry = %#v", entries[1])
	}
}

func TestMigratePNToLIDCachesEmptyPreflightTemporarily(t *testing.T) {
	state := &pnMigrationTestDB{}
	db := sql.OpenDB(&pnMigrationTestConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLStore(
		NewWithDB(db, "postgres", nil),
		types.NewJID("15550000000", types.DefaultUserServer),
	)
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)

	if err := store.MigratePNToLID(context.Background(), pn, lid); err != nil {
		t.Fatal(err)
	}
	if err := store.MigratePNToLID(context.Background(), pn, lid); err != nil {
		t.Fatal(err)
	}
	if state.queries != 1 || state.begins != 0 {
		t.Fatalf("unexpected database work: %d queries, %d transactions", state.queries, state.begins)
	}
	store.migratedPNSessionsCacheLock.Lock()
	store.emptyPNMigrationCache[pn.SignalAddressUser()] = time.Now().Add(-time.Second)
	store.migratedPNSessionsCacheLock.Unlock()
	if err := store.MigratePNToLID(context.Background(), pn, lid); err != nil {
		t.Fatal(err)
	}
	if state.queries != 2 {
		t.Fatalf("expired empty migration cache suppressed a query: %d", state.queries)
	}
	for _, table := range []string{"whatsmeow_sessions", "whatsmeow_identity_keys", "whatsmeow_sender_keys"} {
		if !strings.Contains(state.query, table) {
			t.Fatalf("existence query did not cover %s", table)
		}
	}
	wantArgs := []string{store.JID, "15551234567:%"}
	if len(state.args) != len(wantArgs) {
		t.Fatalf("unexpected existence query argument count %d", len(state.args))
	}
	for i, want := range wantArgs {
		if got := fmt.Sprint(state.args[i].Value); got != want {
			t.Fatalf("existence query argument %d = %q, want %q", i, got, want)
		}
	}
}

func TestMigratePNToLIDUsesSQLitePrefixRange(t *testing.T) {
	state := &pnMigrationTestDB{}
	db := sql.OpenDB(&pnMigrationTestConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLStore(NewWithDB(db, "sqlite3", nil), types.NewJID("15550000000", types.DefaultUserServer))
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)

	if err := store.MigratePNToLID(context.Background(), pn, lid); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{store.JID, "15551234567:", "15551234567;"}
	if len(state.args) != len(wantArgs) {
		t.Fatalf("SQLite preflight argument count = %d, want %d", len(state.args), len(wantArgs))
	}
	for index, want := range wantArgs {
		if got := fmt.Sprint(state.args[index].Value); got != want {
			t.Fatalf("SQLite preflight argument %d = %q, want %q", index, got, want)
		}
	}
}
