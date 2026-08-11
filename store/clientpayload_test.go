// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package store

import "testing"

func TestDevicePropsAdvertiseInlineContacts(t *testing.T) {
	if !DeviceProps.GetHistorySyncConfig().GetSupportInlineContacts() {
		t.Fatal("device properties do not advertise inline contact history")
	}
}
