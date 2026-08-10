//go:build benchmark_legacy

package main

import (
	"context"
	"testing"
)

func TestLegacyBusinessAppValidationIsSkipped(t *testing.T) {
	if err := validateBusinessApp(context.Background(), nil); err != nil {
		t.Fatalf("legacy baseline rejected business smoke validation: %v", err)
	}
}
