// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package binary_test

import (
	"reflect"
	"testing"

	"github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
)

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	want := binary.Node{
		Tag: "iq",
		Attrs: binary.Attrs{
			"id":   "abc",
			"from": types.NewJID("12345", types.DefaultUserServer),
		},
		Content: []binary.Node{{Tag: "body", Content: []byte("hi")}},
	}
	marshaled, err := binary.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	unpacked, err := binary.Unpack(marshaled)
	if err != nil {
		t.Fatal(err)
	}
	got, err := binary.Unmarshal(unpacked)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("round trip mismatch: got %#v, want %#v", *got, want)
	}
}

func TestUnmarshalRejectsMalformedStringTokens(t *testing.T) {
	tests := map[string][]byte{
		"nil node tag":           {248, 1, 0},
		"empty packed tag":       {248, 1, 255, 128},
		"empty packed attribute": {248, 3, 252, 1, 97, 255, 128, 252, 1, 120},
		"JID user list":          {248, 2, 252, 1, 97, 250, 248, 0, 252, 1, 115},
		"JID server list":        {248, 2, 252, 1, 97, 250, 0, 248, 0},
		"AD JID user list":       {248, 2, 252, 1, 97, 247, 1, 1, 248, 0},
		"FB JID user list":       {248, 2, 252, 1, 97, 246, 248, 0, 0, 0, 252, 4, 109, 115, 103, 114},
		"interop user list": {248, 2, 252, 1, 97, 245, 248, 0, 0, 0, 0, 0, 252, 7,
			105, 110, 116, 101, 114, 111, 112},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := binary.Unmarshal(input); err == nil {
				t.Fatal("expected malformed node error")
			}
		})
	}
}

func TestUnpackRejectsEmptyInput(t *testing.T) {
	for _, input := range [][]byte{nil, {}} {
		if _, err := binary.Unpack(input); err == nil {
			t.Fatal("expected empty input error")
		}
	}
}

func FuzzUnmarshal(f *testing.F) {
	for _, input := range [][]byte{
		{248, 1, 0},
		{248, 1, 255, 128},
		{248, 2, 252, 1, 97, 250, 248, 0, 252, 1, 115},
		{},
	} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = binary.Unmarshal(input)
	})
}

func FuzzUnpack(f *testing.F) {
	for _, input := range [][]byte{{}, {0}, {1}, {2, 1, 2, 3}} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = binary.Unpack(input)
	})
}
