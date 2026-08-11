// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package sqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	"github.com/polymorfa/hypermeow/types"
)

type identityReaderState struct {
	queries int
}

type identityReaderConnector struct {
	state *identityReaderState
}

func (c *identityReaderConnector) Connect(context.Context) (driver.Conn, error) {
	return &identityReaderConn{state: c.state}, nil
}

func (*identityReaderConnector) Driver() driver.Driver {
	return identityReaderDriver{}
}

type identityReaderDriver struct{}

func (identityReaderDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type identityReaderConn struct {
	state *identityReaderState
}

func (*identityReaderConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (*identityReaderConn) Close() error {
	return nil
}

func (*identityReaderConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}

func (c *identityReaderConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.state.queries++
	return &identityReaderRows{}, nil
}

type identityReaderRows struct {
	index int
}

func (*identityReaderRows) Columns() []string {
	return []string{"their_id", "identity"}
}

func (*identityReaderRows) Close() error {
	return nil
}

func (r *identityReaderRows) Next(values []driver.Value) error {
	rows := []struct {
		address string
		key     byte
	}{
		{address: "100000000000001:1", key: 0x11},
		{address: "100000000000001:2", key: 0x22},
	}
	if r.index >= len(rows) {
		return io.EOF
	}
	row := rows[r.index]
	r.index++
	values[0] = row.address
	values[1] = bytes.Repeat([]byte{row.key}, 32)
	return nil
}

func TestGetManyIdentitiesUsesOneQueryAndCachesResults(t *testing.T) {
	state := &identityReaderState{}
	db := sql.OpenDB(&identityReaderConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLStore(
		NewWithDB(db, "sqlite3", nil),
		types.NewJID("100000000000000", types.HiddenUserServer),
	)
	addresses := []string{"100000000000001:1", "100000000000001:2"}

	got, _, err := store.GetManyIdentities(context.Background(), addresses)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[addresses[0]][0] != 0x11 || got[addresses[1]][0] != 0x22 {
		t.Fatalf("unexpected identities: %#v", got)
	}
	if _, _, err = store.GetManyIdentities(context.Background(), addresses); err != nil {
		t.Fatal(err)
	}
	if state.queries != 1 {
		t.Fatalf("database queries = %d, want 1", state.queries)
	}
}
