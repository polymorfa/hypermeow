package store

import "testing"

func TestDevicePropsAdvertiseInlineContacts(t *testing.T) {
	if !DeviceProps.GetHistorySyncConfig().GetSupportInlineContacts() {
		t.Fatal("device properties do not advertise inline contact history")
	}
}
