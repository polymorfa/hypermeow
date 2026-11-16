// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package guest

import (
	"context"
	"testing"

	"github.com/polymorfa/hypermeow/store"
)

func TestMemoryIdentityReaderPreservesTrustedKey(t *testing.T) {
	ctx := context.Background()
	device := NewDevice(nil)
	reader := device.Identities.(store.IdentityKeyReader)
	const address = "123:0"
	_, generation, err := reader.GetManyIdentities(ctx, []string{address})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		key     [32]byte
		trusted bool
	}{
		{"first use", [32]byte{1}, true},
		{"matching identity", [32]byte{1}, true},
		{"conflicting identity", [32]byte{2}, false},
		{"original identity retained", [32]byte{1}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			trusted, ensureErr := reader.EnsureIdentity(ctx, address, test.key, generation)
			if ensureErr != nil || trusted != test.trusted {
				t.Fatalf("EnsureIdentity trusted=%v, error=%v, want %v", trusted, ensureErr, test.trusted)
			}
		})
	}
	stored, _, err := reader.GetManyIdentities(ctx, []string{address})
	if err != nil || stored[address] != ([32]byte{1}) {
		t.Fatalf("conflict replaced original identity: %v, %v", stored, err)
	}
}

func TestMemoryIdentityReaderRejectsFetchAfterDeletion(t *testing.T) {
	for _, all := range []bool{false, true} {
		name := "one identity"
		if all {
			name = "all identities"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			device := NewDevice(nil)
			reader := device.Identities.(store.IdentityKeyReader)
			const address = "123:0"
			_, generation, err := reader.GetManyIdentities(ctx, []string{address})
			if err != nil {
				t.Fatal(err)
			}
			if all {
				err = device.Identities.DeleteAllIdentities(ctx, "123")
			} else {
				err = device.Identities.DeleteIdentity(ctx, address)
			}
			if err != nil {
				t.Fatal(err)
			}
			trusted, err := reader.EnsureIdentity(ctx, address, [32]byte{1}, generation)
			if err != nil || trusted {
				t.Fatalf("stale fetch trusted=%v, error=%v", trusted, err)
			}
			stored, freshGeneration, err := reader.GetManyIdentities(ctx, []string{address})
			if err != nil || len(stored) != 0 || freshGeneration == generation {
				t.Fatalf("deleted identity restored: %v, generation=%d, error=%v", stored, freshGeneration, err)
			}
			trusted, err = reader.EnsureIdentity(ctx, address, [32]byte{2}, freshGeneration)
			if err != nil || !trusted {
				t.Fatalf("fresh fetch trusted=%v, error=%v", trusted, err)
			}
		})
	}
}

func TestMemoryIdentityReaderConcurrentFetchesKeepOneKey(t *testing.T) {
	ctx := context.Background()
	device := NewDevice(nil)
	reader := device.Identities.(store.IdentityKeyReader)
	const address = "123:0"
	_, generation, err := reader.GetManyIdentities(ctx, []string{address})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		key     [32]byte
		trusted bool
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, key := range [][32]byte{{1}, {2}} {
		go func() {
			<-start
			trusted, ensureErr := reader.EnsureIdentity(ctx, address, key, generation)
			results <- result{key, trusted, ensureErr}
		}()
	}
	close(start)
	var winner [32]byte
	accepted := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.trusted {
			accepted++
			winner = result.key
		}
	}
	stored, _, err := reader.GetManyIdentities(ctx, []string{address})
	if err != nil || accepted != 1 || stored[address] != winner {
		t.Fatalf("concurrent identities: accepted=%d, stored=%v, error=%v", accepted, stored, err)
	}
}
