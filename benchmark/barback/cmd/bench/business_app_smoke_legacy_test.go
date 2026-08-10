//go:build benchmark_legacy

package main

import (
	"context"
	"testing"
)

func TestLegacyBusinessAppValidationIsSkipped(t *testing.T) {
	if businessAppSmokeSupported() {
		t.Fatal("legacy baseline reported business smoke support")
	}
	if err := validateBusinessApp(context.Background(), nil); err != nil {
		t.Fatalf("legacy baseline rejected business smoke validation: %v", err)
	}
}
