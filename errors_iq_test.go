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

func TestIQErrorIsNormalizesEmptyChildLists(t *testing.T) {
	decoded := &IQError{ErrorNode: &waBinary.Node{Tag: "error"}}
	handBuilt := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Content: []waBinary.Node{}}}
	if !errors.Is(decoded, handBuilt) || !errors.Is(handBuilt, decoded) {
		t.Fatal("empty and nil IQ error child lists did not compare equal")
	}
}

func TestIQErrorIsNormalizesEncodedAttributeScalars(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   any
		encoded string
	}{
		{name: "int", value: int(-30), encoded: "-30"},
		{name: "int32", value: int32(-31), encoded: "-31"},
		{name: "int64", value: int64(-32), encoded: "-32"},
		{name: "uint", value: uint(30), encoded: "30"},
		{name: "uint32", value: uint32(31), encoded: "31"},
		{name: "uint64", value: uint64(32), encoded: "32"},
		{name: "bool", value: false, encoded: "false"},
		{name: "bytes", value: []byte("opaque"), encoded: "opaque"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"value": test.encoded}}}
			handBuilt := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"value": test.value}}}
			if !errors.Is(decoded, handBuilt) || !errors.Is(handBuilt, decoded) {
				t.Fatal("wire-equivalent IQ error attributes did not compare equal")
			}
		})
	}
}

func TestIQErrorIsNormalizesEncodedContentScalars(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   any
		encoded string
	}{
		{name: "string", value: "details", encoded: "details"},
		{name: "int", value: int(-30), encoded: "-30"},
		{name: "int32", value: int32(-31), encoded: "-31"},
		{name: "int64", value: int64(-32), encoded: "-32"},
		{name: "uint", value: uint(30), encoded: "30"},
		{name: "uint32", value: uint32(31), encoded: "31"},
		{name: "uint64", value: uint64(32), encoded: "32"},
		{name: "bool", value: false, encoded: "false"},
		{name: "bytes", value: []byte("opaque"), encoded: "opaque"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Content: []byte(test.encoded)}}
			handBuilt := &IQError{ErrorNode: &waBinary.Node{Tag: "error", Content: test.value}}
			if !errors.Is(decoded, handBuilt) || !errors.Is(handBuilt, decoded) {
				t.Fatal("wire-equivalent IQ error content did not compare equal")
			}
		})
	}
}
