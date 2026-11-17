// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
)

type sessionTxnProbe struct{ commits, rollbacks int }
type sessionTxnConnector struct{ probe *sessionTxnProbe }

func (c sessionTxnConnector) Connect(context.Context) (driver.Conn, error) {
	return sessionTxnConn(c), nil
}
func (sessionTxnConnector) Driver() driver.Driver { return sessionTxnDriver{} }

type sessionTxnDriver struct{}

func (sessionTxnDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use authored connector")
}

type sessionTxnConn struct{ probe *sessionTxnProbe }

func (sessionTxnConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected query")
}
func (sessionTxnConn) Close() error                { return nil }
func (c sessionTxnConn) Begin() (driver.Tx, error) { return sessionTxnHandle(c), nil }

type sessionTxnHandle struct{ probe *sessionTxnProbe }

func (t sessionTxnHandle) Commit() error   { t.probe.commits++; return nil }
func (t sessionTxnHandle) Rollback() error { t.probe.rollbacks++; return nil }

func TestSessionTransactionStateUsesTheActualDatabaseContext(t *testing.T) {
	probe := &sessionTxnProbe{}
	db := sql.OpenDB(sessionTxnConnector{probe})
	t.Cleanup(func() { _ = db.Close() })
	container := NewWithDB(db, "sqlite3", nil)
	backend := NewSQLStore(container, types.NewJID("100000000001", types.DefaultUserServer))
	ctx := context.Background()
	if backend.SessionTransactionActive(ctx) {
		t.Fatal("plain context marked transactional")
	}
	fault := errors.New("authored rollback")
	if err := backend.DoDecryptionTxn(ctx, func(tx context.Context) error {
		if !backend.SessionTransactionActive(tx) || !store.SessionTransactionActive(tx) {
			t.Error("decryption transaction was not marked")
		}
		return fault
	}); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	if err := container.db.DoTxn(ctx, nil, func(tx context.Context) error {
		if !backend.SessionTransactionActive(tx) {
			t.Error("raw database transaction was not detected")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if probe.commits != 1 || probe.rollbacks != 1 {
		t.Fatalf("transaction probe: %+v", probe)
	}
	if backend.SessionTransactionActive(ctx) || store.SessionTransactionActive(ctx) {
		t.Error("transaction state escaped its callback")
	}
}
