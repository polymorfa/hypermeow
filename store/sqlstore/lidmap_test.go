package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

type emptyLIDMapDB struct {
	queries int
	maxArgs int
}

type emptyLIDMapConnector struct{ state *emptyLIDMapDB }

func (connector *emptyLIDMapConnector) Connect(context.Context) (driver.Conn, error) {
	return &emptyLIDMapConn{state: connector.state}, nil
}

func (*emptyLIDMapConnector) Driver() driver.Driver { return pnMigrationTestDriver{} }

type emptyLIDMapConn struct{ state *emptyLIDMapDB }

func (*emptyLIDMapConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (*emptyLIDMapConn) Close() error              { return nil }
func (*emptyLIDMapConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (conn *emptyLIDMapConn) QueryContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	conn.state.queries++
	conn.state.maxArgs = max(conn.state.maxArgs, len(args))
	return emptyLIDMapRows{}, nil
}

type emptyLIDMapRows struct{}

func (emptyLIDMapRows) Columns() []string         { return []string{"lid", "pn"} }
func (emptyLIDMapRows) Close() error              { return nil }
func (emptyLIDMapRows) Next([]driver.Value) error { return io.EOF }

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

func TestGetManyPNsForLIDsCachesMissingMappings(t *testing.T) {
	state := &emptyLIDMapDB{}
	db := sql.OpenDB(&emptyLIDMapConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	cache := NewWithDB(db, "sqlite3", nil).LIDMap
	lid := types.NewJID("100000000000001", types.HiddenUserServer)

	for range 2 {
		got, err := cache.GetManyPNsForLIDs(context.Background(), []types.JID{lid})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("missing mapping returned %#v", got)
		}
	}
	if state.queries != 1 {
		t.Fatalf("missing mapping queried %d times, want 1", state.queries)
	}
}

func TestGetManyPNsForLIDsChunksFallbackQueries(t *testing.T) {
	state := &emptyLIDMapDB{}
	db := sql.OpenDB(&emptyLIDMapConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	cache := NewWithDB(db, "sqlite3", nil).LIDMap
	lids := make([]types.JID, lidQueryBatchSize*2+1)
	for i := range lids {
		lids[i] = types.NewJID(fmt.Sprintf("%d", 100000000000000+i), types.HiddenUserServer)
	}

	if _, err := cache.GetManyPNsForLIDs(context.Background(), lids); err != nil {
		t.Fatal(err)
	}
	if state.queries != 3 {
		t.Fatalf("fallback issued %d queries, want 3", state.queries)
	}
	if state.maxArgs > lidQueryBatchSize {
		t.Fatalf("fallback query used %d arguments, limit %d", state.maxArgs, lidQueryBatchSize)
	}
}
