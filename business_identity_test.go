// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"testing"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/types"
)

func TestParseBusinessProfileAEAAccountType(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	for _, value := range []types.AEAAccountType{
		types.AEAAccountTypeNotSet,
		types.AEAAccountTypeE2EE,
		types.AEAAccountTypeHybridE2EE,
		types.AEAAccountTypeNonE2EE,
	} {
		// <business_profile><profile jid tag><aea_account_type value/>...</profile></business_profile>
		// (WAWebQueryBusinessProfileJob:14-25)
		node := waBinary.Node{
			Tag: "business_profile",
			Content: []waBinary.Node{{
				Tag:   "profile",
				Attrs: waBinary.Attrs{"jid": jid, "tag": "1234567890"},
				Content: []waBinary.Node{
					{Tag: "aea_account_type", Attrs: waBinary.Attrs{"value": string(value)}},
					{Tag: "description", Content: []byte("Synthetic tea shop")},
				},
			}},
		}
		profile, err := (&Client{}).parseBusinessProfile(&node)
		if err != nil {
			t.Fatal(err)
		}
		if profile.AEAAccountType != value {
			t.Fatalf("AEAAccountType = %q, want %q", profile.AEAAccountType, value)
		}
		if want := value == types.AEAAccountTypeHybridE2EE || value == types.AEAAccountTypeNonE2EE; profile.AEAAccountType.IsAEA() != want {
			t.Fatalf("%q IsAEA = %v, want %v", value, !want, want)
		}
	}
}

func TestParseBusinessProfileWithoutAEAAccountType(t *testing.T) {
	node := waBinary.Node{
		Tag: "business_profile",
		Content: []waBinary.Node{{
			Tag:   "profile",
			Attrs: waBinary.Attrs{"jid": types.NewJID("15551234567", types.DefaultUserServer)},
		}},
	}
	profile, err := (&Client{}).parseBusinessProfile(&node)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AEAAccountType != types.AEAAccountTypeUnknown {
		t.Fatalf("AEAAccountType = %q, want empty", profile.AEAAccountType)
	}
}

func TestParseUSyncAEAAccountType(t *testing.T) {
	// <business><verified_name/><profile><aea_account_type value/></profile></business>
	// (WAWebUsyncBusiness:10-20)
	business := waBinary.Node{
		Tag: "business",
		Content: []waBinary.Node{
			{Tag: "verified_name"},
			{Tag: "profile", Content: []waBinary.Node{
				{Tag: "aea_account_type", Attrs: waBinary.Attrs{"value": "hybrid_e2ee"}},
			}},
		},
	}
	if got := parseUSyncAEAAccountType(business); got != types.AEAAccountTypeHybridE2EE {
		t.Fatalf("usync AEA = %q, want hybrid_e2ee", got)
	}
	if got := parseUSyncAEAAccountType(waBinary.Node{Tag: "business"}); got != types.AEAAccountTypeUnknown {
		t.Fatalf("usync AEA without profile = %q, want empty", got)
	}
	if got := parseUSyncAEAAccountType(waBinary.Node{}); got != types.AEAAccountTypeUnknown {
		t.Fatalf("usync AEA without business = %q, want empty", got)
	}
}
