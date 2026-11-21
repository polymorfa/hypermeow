// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"bytes"
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
)

func TestPreKeyDeviceIdentity(t *testing.T) {
	identity := []byte{0x0a, 0x01, 0x02}
	cases := []struct {
		name string
		node waBinary.Node
		want []byte
	}{
		{
			name: "beside the identity key",
			node: waBinary.Node{Tag: "user", Content: []waBinary.Node{
				{Tag: "registration", Content: []byte{0, 0, 0, 1}},
				{Tag: "device-identity", Content: identity},
			}},
			want: identity,
		},
		{
			name: "inside a keys wrapper",
			node: waBinary.Node{Tag: "user", Content: []waBinary.Node{
				{Tag: "keys", Content: []waBinary.Node{{Tag: "device-identity", Content: identity}}},
			}},
			want: identity,
		},
		{
			name: "primary device without one",
			node: waBinary.Node{Tag: "user", Content: []waBinary.Node{
				{Tag: "registration", Content: []byte{0, 0, 0, 1}},
			}},
		},
		{
			name: "empty element",
			node: waBinary.Node{Tag: "user", Content: []waBinary.Node{{Tag: "device-identity"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := preKeyDeviceIdentity(tc.node); !bytes.Equal(got, tc.want) {
				t.Fatalf("preKeyDeviceIdentity = %x, want %x", got, tc.want)
			}
		})
	}
}
