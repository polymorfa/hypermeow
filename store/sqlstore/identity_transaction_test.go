//go:build cgo

// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package sqlstore_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/store/sqlstore"
	"github.com/polymorfa/hypermeow/types"
)

// Authored SQL rollback example. Opaque session bytes are not live Signal records.
func TestSQLIdentityRollbackReplay(t *testing.T) {
	for _, scope := range []string{"chat", "status"} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			db, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			for _, q := range []string{
				"CREATE TABLE whatsmeow_identity_keys (our_jid TEXT, their_id TEXT, identity BLOB, PRIMARY KEY(our_jid,their_id))",
				"CREATE TABLE whatsmeow_sessions (our_jid TEXT, their_id TEXT, session BLOB, PRIMARY KEY(our_jid,their_id))",
			} {
				if _, err = db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			s := sqlstore.NewSQLStore(sqlstore.NewWithDB(db, "sqlite3", nil), types.NewJID("100000000001", types.DefaultUserServer))
			address := "100000000002:1"
			a, b := [32]byte{1}, [32]byte{2}
			if err = s.PutIdentity(ctx, address, a); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{address, store.SignalSessionAddress(store.WithStatusSession(ctx), address)} {
				if err = s.PutSession(ctx, key, []byte("old")); err != nil {
					t.Fatal(err)
				}
			}
			fault := errors.New("authored buffered-event persistence failure")
			err = s.DoDecryptionTxn(ctx, func(tx context.Context) error {
				if e := s.DeleteIdentity(tx, address); e != nil {
					return e
				}
				if e := s.PutIdentity(tx, address, b); e != nil {
					return e
				}
				key := address
				if scope == "status" {
					key = store.SignalSessionAddress(store.WithStatusSession(ctx), address)
				}
				if e := s.PutSession(tx, key, []byte("new")); e != nil {
					return e
				}
				return fault
			})
			if !errors.Is(err, fault) {
				t.Fatal(err)
			}
			for _, key := range []string{address, store.SignalSessionAddress(store.WithStatusSession(ctx), address)} {
				got, err := s.GetSession(ctx, key)
				if err != nil || string(got) != "old" {
					t.Fatalf("rollback session %s = %q, %v", key, got, err)
				}
			}
			trusted, err := s.IsTrustedIdentity(ctx, address, b)
			if err != nil {
				t.Fatal(err)
			}
			if trusted {
				t.Error("rolled-back replacement identity remained trusted")
			}
			// Replay must write B instead of skipping its write because of stale cache state.
			if err = s.DoDecryptionTxn(ctx, func(tx context.Context) error {
				if err := s.PutIdentity(tx, address, b); err != nil {
					return err
				}
				keys, generation, err := s.GetManyIdentities(tx, []string{address})
				if err != nil {
					return err
				}
				if keys[address] != b {
					t.Fatal("transaction read did not see replacement identity")
				}
				accepted, err := s.EnsureIdentity(tx, address, b, generation)
				if err == nil && !accepted {
					t.Fatal("transaction rejected its own stored identity")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var got []byte
			if err = db.QueryRow("SELECT identity FROM whatsmeow_identity_keys WHERE our_jid=? AND their_id=?", s.JID, address).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if trusted, err := s.IsTrustedIdentity(ctx, address, a); err != nil || trusted {
				t.Fatalf("committed replacement did not invalidate A: %v, %v", trusted, err)
			}
			if len(got) != 32 || got[0] != 2 {
				t.Errorf("replay persisted identity %x, want B", got)
			}
		})
	}
}
