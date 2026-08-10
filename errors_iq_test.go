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

func TestIQErrorIsNormalizesEquivalentAttributes(t *testing.T) {
	decoded := &IQError{ErrorNode: &waBinary.Node{Tag: "error"}}
	handBuilt := &IQError{ErrorNode: &waBinary.Node{
		Tag:   "error",
		Attrs: waBinary.Attrs{"ignored-empty": "", "ignored-nil": nil},
		Content: []waBinary.Node{{
			Tag:   "detail",
			Attrs: waBinary.Attrs{},
		}},
	}}
	decoded.ErrorNode.Content = []waBinary.Node{{Tag: "detail"}}
	if !errors.Is(decoded, handBuilt) || !errors.Is(handBuilt, decoded) {
		t.Fatal("semantically equivalent IQ error nodes did not compare equal")
	}
}
