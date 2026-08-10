//go:build benchmark_legacy

package main

import (
	"context"
	"testing"

	"github.com/polymorfa/hypermeow/types"
)

func TestLegacySecurityCodeValidationIsSkipped(t *testing.T) {
	if err := validateIdentityVerificationCodes(context.Background(), nil, types.EmptyJID); err != nil {
		t.Fatalf("legacy baseline rejected security-code smoke validation: %v", err)
	}
}
