package whatsmeow

import (
	"testing"

	"github.com/polymorfa/hypermeow/types"
)

func TestApplyPrivacySettingUpdatesEveryCategory(t *testing.T) {
	tests := []struct {
		name  types.PrivacySettingType
		value types.PrivacySetting
		get   func(types.PrivacySettings) types.PrivacySetting
	}{
		{types.PrivacySettingTypeGroupAdd, types.PrivacySettingContacts, func(s types.PrivacySettings) types.PrivacySetting { return s.GroupAdd }},
		{types.PrivacySettingTypeLastSeen, types.PrivacySettingContacts, func(s types.PrivacySettings) types.PrivacySetting { return s.LastSeen }},
		{types.PrivacySettingTypeStatus, types.PrivacySettingContacts, func(s types.PrivacySettings) types.PrivacySetting { return s.Status }},
		{types.PrivacySettingTypeProfile, types.PrivacySettingContacts, func(s types.PrivacySettings) types.PrivacySetting { return s.Profile }},
		{types.PrivacySettingTypeReadReceipts, types.PrivacySettingNone, func(s types.PrivacySettings) types.PrivacySetting { return s.ReadReceipts }},
		{types.PrivacySettingTypeOnline, types.PrivacySettingMatchLastSeen, func(s types.PrivacySettings) types.PrivacySetting { return s.Online }},
		{types.PrivacySettingTypeCallAdd, types.PrivacySettingKnown, func(s types.PrivacySettings) types.PrivacySetting { return s.CallAdd }},
		{types.PrivacySettingTypeMessages, types.PrivacySettingContacts, func(s types.PrivacySettings) types.PrivacySetting { return s.Messages }},
		{types.PrivacySettingTypeDefense, types.PrivacySettingOnStandard, func(s types.PrivacySettings) types.PrivacySetting { return s.Defense }},
		{types.PrivacySettingTypeStickers, types.PrivacySettingContactAllowlist, func(s types.PrivacySettings) types.PrivacySetting { return s.Stickers }},
	}

	for _, test := range tests {
		t.Run(string(test.name), func(t *testing.T) {
			var settings types.PrivacySettings
			applyPrivacySetting(&settings, test.name, test.value)
			if actual := test.get(settings); actual != test.value {
				t.Fatalf("setting = %q, want %q", actual, test.value)
			}
		})
	}
}
