// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package guest

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/polymorfa/hypermeow/proto/waWa6"
	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
)

// newTestDevice is a NewDevice with fixed registration and prekey IDs, so the
// key encodings can be checked byte for byte.
func newTestDevice() *store.Device {
	device := NewDevice(nil)
	device.RegistrationID = 0x01020304
	device.SignedPreKey = device.IdentityKey.CreateSignedPreKey(7)
	return device
}

func decodeKey(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.NewReplacer("-", "+", "_", "/").Replace(value))
	if err != nil {
		t.Fatalf("decode %q: %v", value, err)
	}
	return raw
}

func TestRegisterSendsKeysAndParsesLID(t *testing.T) {
	device := newTestDevice()
	var rawQuery string
	var header http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/guest_reg" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		rawQuery, header = r.URL.RawQuery, r.Header.Clone()
		_, _ = w.Write([]byte(`{"status":"ok","lid":"123456789","ent_access_token":"T","ent_canonical_fbid":"F"}`))
	}))
	defer server.Close()

	reg, err := Register(context.Background(), device, Request{
		InviteCode: "https://call.whatsapp.com/voice/AbC123", Name: "Ada Lovelace",
		BaseURL: server.URL + "/v2", HTTPClient: server.Client(), Language: "pt", PrivateInviteCode: "PRIV",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if reg.LID != types.NewJID("123456789", types.HiddenUserServer) || reg.Name != "Ada Lovelace" ||
		reg.EntAccessToken != "T" || reg.EntCanonicalFBID != "F" {
		t.Fatalf("registration = %+v", reg)
	}

	if !strings.Contains(rawQuery, "guest_name=Ada%20Lovelace") {
		t.Fatalf("guest_name must use %%20 for spaces: %s", rawQuery)
	}
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if query.Get("lg") != "PT" || query.Get("lc") != "US" || query.Get("private_invite_code") != "PRIV" ||
		query.Get("mcc") != "310" || query.Get("mnc") != "999" || query.Get("tos_version") != "1" {
		t.Fatalf("defaults and options = %v", query)
	}
	if query.Get("invite_code") != "AbC123" || query.Get("guest_type") != "guest_calling" {
		t.Fatalf("invite = %q type = %q", query.Get("invite_code"), query.Get("guest_type"))
	}
	if got := decodeKey(t, query.Get("authkey")); string(got) != string(device.NoiseKey.Pub[:]) {
		t.Fatal("authkey is not the noise public key")
	}
	if got := decodeKey(t, query.Get("e_ident")); string(got) != string(device.IdentityKey.Pub[:]) {
		t.Fatal("e_ident is not the identity public key")
	}
	if got := decodeKey(t, query.Get("e_regid")); string(got) != "\x01\x02\x03\x04" {
		t.Fatalf("e_regid = %x", got)
	}
	if got := decodeKey(t, query.Get("e_skey_id")); string(got) != "\x00\x00\x07" {
		t.Fatalf("e_skey_id = %x", got)
	}
	if got := decodeKey(t, query.Get("e_keytype")); string(got) != "\x05" {
		t.Fatalf("e_keytype = %x", got)
	}
	if got := decodeKey(t, query.Get("e_skey_sig")); string(got) != string(device.SignedPreKey.Signature[:]) {
		t.Fatal("e_skey_sig is not the signed prekey signature")
	}
	if len(query.Get("id")) != 20 || query.Get("id") != query.Get("backup_token") {
		t.Fatalf("id and backup_token must be the same 20 raw bytes")
	}
	if header.Get("User-Agent") != DefaultBrowser.UserAgent || header.Get("Origin") != DefaultOrigin ||
		header.Get("Referer") != DefaultOrigin+"/" {
		t.Fatalf("headers = %v", header)
	}
}

func TestRegisterReportsRejections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":"fail","reason":"blocked","param":"invite_code","retry_after":30}`))
	}))
	defer server.Close()
	_, err := Register(context.Background(), newTestDevice(), Request{
		InviteCode: "TOKEN", Name: "Guest", BaseURL: server.URL, HTTPClient: server.Client(),
	})
	var regErr *Error
	if !errors.As(err, &regErr) || regErr.HTTPStatus != http.StatusForbidden || regErr.Reason != "blocked" ||
		regErr.Param != "invite_code" || regErr.RetryAfter != "30" {
		t.Fatalf("error = %v", err)
	}
}

func TestRegisterRejectsBadInput(t *testing.T) {
	if _, err := Register(context.Background(), &store.Device{}, Request{InviteCode: "T", Name: "G"}); !errors.Is(err, ErrMissingKeys) {
		t.Fatalf("missing keys error = %v", err)
	}
	if _, err := Register(context.Background(), newTestDevice(), Request{InviteCode: "T", Name: "  "}); err == nil {
		t.Fatal("a blank name must be rejected")
	}
	if _, err := ParseInviteCode("https://call.whatsapp.com/"); err == nil {
		t.Fatal("a link without a token must be rejected")
	}
}

func TestRegisterRejectsMalformedLID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","lid":"not-a-number"}`))
	}))
	defer server.Close()
	if _, err := Register(context.Background(), newTestDevice(), Request{
		InviteCode: "T", Name: "G", BaseURL: server.URL, HTTPClient: server.Client(),
	}); err == nil {
		t.Fatal("a malformed lid must be rejected")
	}
}

func TestNewDeviceHasWorkingMemoryStores(t *testing.T) {
	device := NewDevice(nil)
	if device.ID != nil || !device.Initialized || device.PreKeys == nil || device.Sessions == nil ||
		device.Identities == nil || device.PrivacyTokens == nil || device.LIDs == nil || device.Container == nil {
		t.Fatalf("device stores = %+v", device)
	}
	ctx := context.Background()
	if err := device.Save(ctx); err != nil {
		t.Fatalf("Save must be a no-op: %v", err)
	}
	if err := device.Sessions.PutSession(ctx, "1.0", []byte("s")); err != nil {
		t.Fatal(err)
	}
	if got, _ := device.Sessions.GetSession(ctx, "1.0"); string(got) != "s" {
		t.Fatalf("session = %q", got)
	}
	preKeys, err := device.PreKeys.GetOrGenPreKeys(ctx, 3)
	if err != nil || len(preKeys) != 3 {
		t.Fatalf("prekeys = %v, %v", preKeys, err)
	}
	if count, _ := device.PreKeys.UploadedPreKeyCount(ctx); count != 0 {
		t.Fatalf("unuploaded prekeys counted: %d", count)
	}
	if err = device.PreKeys.MarkPreKeysAsUploaded(ctx, preKeys[2].KeyID); err != nil {
		t.Fatal(err)
	}
	if count, _ := device.PreKeys.UploadedPreKeyCount(ctx); count != 3 {
		t.Fatalf("uploaded prekeys = %d", count)
	}
	if trusted, _ := device.Identities.IsTrustedIdentity(ctx, "1.0", [32]byte{1}); !trusted {
		t.Fatal("an unknown identity must be trusted on first use")
	}
	_ = device.Identities.PutIdentity(ctx, "1.0", [32]byte{1})
	if trusted, _ := device.Identities.IsTrustedIdentity(ctx, "1.0", [32]byte{2}); trusted {
		t.Fatal("a changed identity must not be trusted")
	}
}

func TestNewClientStartsAuthenticatedWithGuestPayload(t *testing.T) {
	device := NewDevice(nil)
	reg := &Registration{
		LID: types.NewJID("123456789", types.HiddenUserServer), Name: "Ada", Browser: DefaultBrowser,
	}
	cli, err := NewClient(device, reg, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if device.ID == nil || *device.ID != reg.LID || device.LID != reg.LID || device.PushName != "Ada" {
		t.Fatalf("device identity = %v / %v / %q", device.ID, device.LID, device.PushName)
	}
	if !cli.DisablePostConnectPassiveIQ || cli.UserAgent != DefaultBrowser.UserAgent || cli.GetClientPayload == nil {
		t.Fatal("client was not configured for a guest login")
	}
	payload := cli.GetClientPayload()
	if payload.GetUsername() != 123456789 || payload.GetDevice() != 0 ||
		payload.GetProduct() != waWa6.ClientPayload_WHATSAPP_LID ||
		payload.GetAccountType() != waWa6.ClientPayload_GUEST ||
		payload.GetPassive() || !payload.GetPull() || payload.GetPushName() != "Ada" ||
		payload.GetDevicePairingData() != nil {
		t.Fatalf("payload = %v", payload)
	}
	if store.BaseClientPayload.AccountType != nil || store.BaseClientPayload.Product != nil {
		t.Fatal("LoginPayload must not modify the shared base payload")
	}
	if _, err = NewClient(&store.Device{}, reg, nil); err == nil {
		t.Fatal("a device without stores must be rejected")
	}
}

func TestRegisterRejectsPairedDeviceAndRedactsURL(t *testing.T) {
	paired := NewDevice(nil)
	id := types.NewJID("1", types.DefaultUserServer)
	paired.ID = &id
	if _, err := Register(context.Background(), paired, Request{InviteCode: "T", Name: "G"}); err == nil {
		t.Fatal("a paired device must be rejected")
	}
	// Nothing listens on this port, so the request fails with a *url.Error.
	_, err := Register(context.Background(), NewDevice(nil), Request{
		InviteCode: "SECRETTOKEN", Name: "Secret Name", BaseURL: "http://127.0.0.1:1",
	})
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") || strings.Contains(err.Error(), "Secret") {
		t.Fatalf("error must not carry the request URL: %v", err)
	}
}

func TestGuestStoreReadsDoNotExposeMutableSessionState(t *testing.T) {
	ctx := context.Background()
	device := NewDevice(nil)
	_ = device.Sessions.PutSession(ctx, "1.0", []byte("session"))
	first, _ := device.Sessions.GetSession(ctx, "1.0")
	first[0] = 'X'
	many, _ := device.Sessions.GetManySessions(ctx, []string{"1.0", "missing"})
	many["1.0"][0] = 'Y'
	again, _ := device.Sessions.GetSession(ctx, "1.0")
	if string(again) != "session" {
		t.Fatal("session changed through returned slice")
	}
	if _, exists := many["missing"]; exists {
		t.Fatal("missing session returned as an existing key")
	}
	_ = device.SenderKeys.PutSenderKey(ctx, "g", "1.0", []byte("sender"))
	sender, _ := device.SenderKeys.GetSenderKey(ctx, "g", "1.0")
	sender[0] = 'X'
	again, _ = device.SenderKeys.GetSenderKey(ctx, "g", "1.0")
	if string(again) != "sender" {
		t.Fatal("sender key changed through returned slice")
	}
}
func TestGuestIdentityCannotReplaceAnAccount(t *testing.T) {
	for _, jid := range []types.JID{types.NewJID("", types.HiddenUserServer), types.NewJID("0", types.HiddenUserServer), types.NewJID("invalid", types.HiddenUserServer), types.NewJID("123", types.DefaultUserServer)} {
		if err := Apply(NewDevice(nil), &Registration{LID: jid}); err == nil {
			t.Fatal("invalid guest identity accepted")
		}
	}
	device := NewDevice(nil)
	account := types.NewJID("123", types.DefaultUserServer)
	device.ID = &account
	if err := Apply(device, &Registration{LID: types.NewJID("456", types.HiddenUserServer)}); err == nil || *device.ID != account {
		t.Fatal("existing account replaced")
	}
}
