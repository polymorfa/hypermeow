// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	waBinary "github.com/polymorfa/hypermeow/binary"
	"github.com/polymorfa/hypermeow/proto/waVnameCert"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
	waLog "github.com/polymorfa/hypermeow/util/log"
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

func buildVerifiedNameCert(t *testing.T, issuer string, serial uint64) []byte {
	t.Helper()
	details, err := proto.Marshal(&waVnameCert.VerifiedNameCertificate_Details{
		Serial:       proto.Uint64(serial),
		Issuer:       proto.String(issuer),
		VerifiedName: proto.String("Synthetic Tea"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := proto.Marshal(&waVnameCert.VerifiedNameCertificate{Details: details})
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestParseVerifiedNameIssuerLevelSerialAndPrivacyMode(t *testing.T) {
	// <verified_name verified_level serial actual_actors host_storage privacy_mode_ts>cert</verified_name>
	// (WAWebCommonParsersVerifiedName:19-32, WAWebValidateBusinessCertificate:6-24)
	node := waBinary.Node{
		Tag: "verified_name",
		Attrs: waBinary.Attrs{
			"verified_level":  "high",
			"serial":          "998877",
			"actual_actors":   "3",
			"host_storage":    "2",
			"privacy_mode_ts": "1700000000",
			"v":               "2",
		},
		Content: buildVerifiedNameCert(t, "ent:wa", 112233),
	}
	vn, err := parseVerifiedNameContent(node)
	if err != nil {
		t.Fatal(err)
	}
	if vn.Issuer != types.VerifiedNameIssuerAPI || !vn.IsAPI() || vn.IsBusinessApp() {
		t.Fatalf("issuer = %q, IsAPI=%v IsBusinessApp=%v", vn.Issuer, vn.IsAPI(), vn.IsBusinessApp())
	}
	if vn.VerifiedLevel != types.VerifiedLevelHigh || vn.Serial != "998877" || vn.Version != 2 {
		t.Fatalf("level/serial/version = %q/%q/%d", vn.VerifiedLevel, vn.Serial, vn.Version)
	}
	want := types.BusinessPrivacyMode{
		ActualActors: types.BusinessActualActorsCAPI,
		HostStorage:  types.BusinessHostStorageFacebook,
		Timestamp:    time.Unix(1700000000, 0),
	}
	if vn.PrivacyMode == nil || *vn.PrivacyMode != want {
		t.Fatalf("privacy mode = %+v, want %+v", vn.PrivacyMode, want)
	}
	if vn.ActualActors != 3 || vn.HostStorage != 2 || !vn.PrivacyModeTS.Equal(want.Timestamp) {
		t.Fatalf("legacy fields changed: %d/%d/%v", vn.ActualActors, vn.HostStorage, vn.PrivacyModeTS)
	}
}

func TestParseVerifiedNameBusinessAppFallsBackToCertificateSerial(t *testing.T) {
	node := waBinary.Node{
		Tag:     "verified_name",
		Attrs:   waBinary.Attrs{"verified_level": "low"},
		Content: buildVerifiedNameCert(t, "smb:wa", 112233),
	}
	vn, err := parseVerifiedNameContent(node)
	if err != nil {
		t.Fatal(err)
	}
	if !vn.IsBusinessApp() || vn.IsAPI() || vn.Serial != "112233" || vn.VerifiedLevel != types.VerifiedLevelLow {
		t.Fatalf("unexpected verified name: issuer=%q serial=%q level=%q", vn.Issuer, vn.Serial, vn.VerifiedLevel)
	}
	if vn.PrivacyMode != nil {
		t.Fatalf("privacy mode = %+v, want nil without attributes", vn.PrivacyMode)
	}
}

func TestParseBusinessPrivacyModeRequiresKnownCompleteValues(t *testing.T) {
	for _, attrs := range []waBinary.Attrs{
		{"actual_actors": "2", "host_storage": "2"},
		{"actual_actors": "2", "privacy_mode_ts": "1700000000"},
		{"actual_actors": "4", "host_storage": "2", "privacy_mode_ts": "1700000000"},
		{"actual_actors": "1", "host_storage": "3", "privacy_mode_ts": "1700000000"},
	} {
		if mode := parseBusinessPrivacyMode(waBinary.Node{Tag: "biz", Attrs: attrs}); mode != nil {
			t.Fatalf("attrs %v parsed as %+v, want nil", attrs, mode)
		}
	}
}

func newBusinessIdentityTestClient() *Client {
	own := types.NewJID("15550000000", types.DefaultUserServer)
	own.Device = 3
	return NewClient(&store.Device{ID: &own, LIDs: &store.NoopStore{}}, waLog.Noop)
}

func TestParseMessageInfoBizPrivacyModeAndVerifiedAttrs(t *testing.T) {
	cli := newBusinessIdentityTestClient()
	business := types.NewJID("15551234567", types.DefaultUserServer)
	// <message verified_level verified_name><verified_name>cert</verified_name><biz .../></message>
	// (WAWebHandleMsgParser:309-327)
	node := &waBinary.Node{
		Tag: "message",
		Attrs: waBinary.Attrs{
			"from":           business,
			"id":             "3EB0BIZ",
			"t":              "1700000100",
			"type":           "text",
			"verified_level": "high",
			"verified_name":  "445566",
		},
		Content: []waBinary.Node{
			{Tag: "verified_name", Content: buildVerifiedNameCert(t, "smb:wa", 112233)},
			{Tag: "biz", Attrs: waBinary.Attrs{"actual_actors": "2", "host_storage": "1", "privacy_mode_ts": "1700000000"}},
			{Tag: "enc", Attrs: waBinary.Attrs{"type": "msg", "v": "2"}},
		},
	}
	info, err := cli.parseMessageInfo(node)
	if err != nil {
		t.Fatal(err)
	}
	want := types.BusinessPrivacyMode{
		ActualActors: types.BusinessActualActorsBSP,
		HostStorage:  types.BusinessHostStorageOnPremise,
		Timestamp:    time.Unix(1700000000, 0),
	}
	if info.BizPrivacyMode == nil || *info.BizPrivacyMode != want {
		t.Fatalf("biz privacy mode = %+v, want %+v", info.BizPrivacyMode, want)
	}
	if info.VerifiedName == nil || info.VerifiedName.VerifiedLevel != types.VerifiedLevelHigh || info.VerifiedName.Serial != "445566" || !info.VerifiedName.IsBusinessApp() {
		t.Fatalf("verified name = %+v", info.VerifiedName)
	}
}

func TestParseMessageInfoIgnoresBotBizPrivacyMode(t *testing.T) {
	cli := newBusinessIdentityTestClient()
	node := &waBinary.Node{
		Tag: "message",
		Attrs: waBinary.Attrs{
			"from": types.NewJID("867051314767696", types.BotServer),
			"id":   "3EB0BOT",
			"t":    "1700000100",
		},
		Content: []waBinary.Node{
			{Tag: "biz", Attrs: waBinary.Attrs{"actual_actors": "2", "host_storage": "1", "privacy_mode_ts": "1700000000"}},
		},
	}
	info, err := cli.parseMessageInfo(node)
	if err != nil {
		t.Fatal(err)
	}
	if info.BizPrivacyMode != nil {
		t.Fatalf("bot message privacy mode = %+v, want nil", info.BizPrivacyMode)
	}
}

func TestUnavailableHostedMessageEvent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs waBinary.Attrs
		want  bool
	}{
		// <unavailable hosted="true"/> (WAWebHandleMsgParser:245-248)
		{"hosted", waBinary.Attrs{"hosted": "true"}, true},
		{"view once", waBinary.Attrs{"type": "view_once"}, false},
		{"plain", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli, _, sent := newTestRemote(t)
			var got []*events.UndecryptableMessage
			cli.AddEventHandler(func(evt any) {
				if e, ok := evt.(*events.UndecryptableMessage); ok {
					got = append(got, e)
				}
			})
			sender := types.NewJID("15551234567", types.DefaultUserServer)
			info := &types.MessageInfo{MessageSource: types.MessageSource{Chat: sender, Sender: sender}, ID: "3EB0HOSTED"}
			node := &waBinary.Node{
				Tag:     "message",
				Attrs:   waBinary.Attrs{"id": "3EB0HOSTED", "from": sender, "t": "1700000000", "type": "text"},
				Content: []waBinary.Node{{Tag: "unavailable", Attrs: tc.attrs}},
			}
			cli.decryptMessages(context.Background(), info, node)
			if len(got) != 1 || !got[0].IsUnavailable {
				t.Fatalf("UndecryptableMessage events = %+v, want one unavailable event", got)
			}
			if got[0].IsHostedUnavailable != tc.want {
				t.Fatalf("IsHostedUnavailable = %v, want %v", got[0].IsHostedUnavailable, tc.want)
			}
			if tags := sent.tags(); !slices.ContainsFunc(tags, func(tag string) bool { return strings.HasPrefix(tag, "ack:") }) {
				t.Fatalf("sent %v, want the stanza acknowledged", tags)
			}
		})
	}
}
