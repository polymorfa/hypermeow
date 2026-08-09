package whatsmeow

import (
	"errors"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
)

func TestIQErrorIsDistinguishesSensitiveAttributes(t *testing.T) {
	first := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"token": "first"}}}
	second := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"token": "second"}}}
	if errors.Is(first, second) {
		t.Fatal("errors with distinct sensitive attributes compare equal")
	}
}
