// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

// Package guest registers a one-time WhatsApp guest identity for a call link
// and logs it in without pairing a phone.
//
// A guest is what WhatsApp creates when someone opens a call link in a browser
// without an account. The identity is scoped to one call link and is not meant
// to be persisted or reused.
//
// Evidence status. The request and response follow the public WhatsApp Web
// guest bundle, build 1053925913 (observed 2026-11-10):
// WAWebGuestCoreBackendConsts.js:2-26 (endpoint and defaults),
// WAWebGuestCoreRegistrationNetworkRequest.js:3-50 (fields and encodings; the
// call-link form is register_with_link_token_and_push_name, invite_code plus
// guest_name) and WAWebGuestCoreRegistrationAction.js:18-48 (response fields
// and failure reason/param/retry_after). The login payload follows
// WAWebGuestCoreBackendBootstrap.js:7-14. The guest_type value guest_calling
// is from a captured call guest session; the calling caller is not in that
// bundle. None of this has been exercised against the server.
package guest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
)

// DefaultBaseURL is the registration host a browser guest uses.
const DefaultBaseURL = "https://v.whatsapp.net/v2"

// DefaultOrigin is the page origin a browser guest registers from.
const DefaultOrigin = "https://web.whatsapp.com"

// Browser describes the browser a guest registers and connects as. Keep it
// consistent with the WebSocket User-Agent; NewClient applies UserAgent to the
// client for that reason.
type Browser struct {
	OSName         string
	OSVersion      string
	Name           string
	Version        string
	UserAgent      string
	AcceptLanguage string
}

// DefaultBrowser is a current desktop Chrome on macOS.
var DefaultBrowser = Browser{
	OSName:         "Mac OS",
	OSVersion:      "10.15.7",
	Name:           "Chrome",
	Version:        "142.0.0.0",
	UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36",
	AcceptLanguage: "en-US,en;q=0.9",
}

// Request is one guest registration for one call link.
type Request struct {
	// InviteCode is the call-link token (not the full URL; see ParseInviteCode).
	InviteCode string
	// Name is the display name other participants see.
	Name string

	// BaseURL overrides DefaultBaseURL, for example to point at a simulator.
	BaseURL string
	// Origin overrides DefaultOrigin for the Origin and Referer headers.
	Origin string
	// Browser overrides DefaultBrowser.
	Browser *Browser
	// Country and Language are the lc and lg parameters. Defaults: "US", "EN".
	// Language is sent uppercased, as WhatsApp Web does.
	Country  string
	Language string
	// PrivateInviteCode is the optional private_invite_code parameter (the
	// page's ic query value).
	PrivateInviteCode string
	// AccessSessionID groups this registration with the rest of the guest
	// session. A random UUID is used when empty.
	AccessSessionID string
	// HTTPClient supplies the transport, timeout and cookie jar, with
	// http.DefaultClient used when nil. Registration never follows redirects;
	// the caller's client is not mutated.
	HTTPClient *http.Client
}

// Registration is a successful guest registration.
type Registration struct {
	// LID is the guest's own identity, assigned at registration.
	LID types.JID
	// Name is the display name the guest registered with.
	Name string
	// EntAccessToken and EntCanonicalFBID are returned by the server and kept
	// for callers that need them; nothing in this package uses them.
	EntAccessToken   string
	EntCanonicalFBID string
	// Browser is the browser the guest registered as.
	Browser Browser
}

// Error is a non-successful registration response.
type Error struct {
	HTTPStatus int
	Status     string
	Reason     string
	// Param names the request field the server objected to, when given.
	Param string
	// RetryAfter is the server's retry_after value as sent, when given.
	RetryAfter string
	Body       string
}

func (e *Error) Error() string {
	switch {
	case e.Reason != "":
		return fmt.Sprintf("guest: registration failed: HTTP %d, %s (%s)", e.HTTPStatus, e.Status, e.Reason)
	case e.Status != "":
		return fmt.Sprintf("guest: registration failed: HTTP %d, %s", e.HTTPStatus, e.Status)
	default:
		return fmt.Sprintf("guest: registration failed: HTTP %d", e.HTTPStatus)
	}
}

// ErrMissingKeys is returned when the device has no registration key material.
var ErrMissingKeys = errors.New("guest: device is missing noise, identity or signed prekey material")

// ParseInviteCode accepts a call-link token or a full call.whatsapp.com URL
// and returns the token.
func ParseInviteCode(tokenOrURL string) (string, error) {
	value := strings.TrimSpace(tokenOrURL)
	if value == "" {
		return "", errors.New("guest: invite code is required")
	}
	if !strings.Contains(value, "/") {
		return value, nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("guest: parse call link: %w", err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	token := segments[len(segments)-1]
	if token == "" {
		return "", errors.New("guest: call link has no token")
	}
	return token, nil
}

// Register registers device as a guest for one call link. Use a fresh device
// from NewDevice, then pass the result to NewClient.
func Register(ctx context.Context, device *store.Device, req Request) (*Registration, error) {
	if device == nil || device.NoiseKey == nil || device.IdentityKey == nil || device.SignedPreKey == nil {
		return nil, ErrMissingKeys
	}
	if device.ID != nil {
		return nil, errors.New("guest: device is already paired; use a fresh device")
	}
	inviteCode, err := ParseInviteCode(req.InviteCode)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("guest: display name is required")
	}
	browser := DefaultBrowser
	if req.Browser != nil {
		browser = *req.Browser
	}
	endpoint, err := registrationURL(device, req, inviteCode, name, browser)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("guest: build registration request: %w", err)
	}
	origin := req.Origin
	if origin == "" {
		origin = DefaultOrigin
	}
	httpReq.Header.Set("User-Agent", browser.UserAgent)
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("Accept-Language", browser.AcceptLanguage)
	httpReq.Header.Set("Origin", origin)
	httpReq.Header.Set("Referer", origin+"/")
	httpReq.Header.Set("Sec-Fetch-Site", "same-site")
	httpReq.Header.Set("Sec-Fetch-Mode", "cors")
	httpReq.Header.Set("Sec-Fetch-Dest", "empty")
	client := req.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	registrationClient := *client
	registrationClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("guest: registration redirects are not allowed")
	}
	resp, err := registrationClient.Do(httpReq)
	if err != nil {
		// The request URL carries the invite code, the name and key material;
		// keep only the cause so callers can log the error safely.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("guest: send registration: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("guest: read registration response: %w", err)
	}
	return parseRegistration(resp.StatusCode, body, name, browser)
}

func registrationURL(device *store.Device, req Request, inviteCode, name string, browser Browser) (string, error) {
	base := req.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	country, language := req.Country, req.Language
	if country == "" {
		country = "US"
	}
	if language == "" {
		language = "EN"
	}
	language = strings.ToUpper(language)
	sessionID := req.AccessSessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	deviceID := make([]byte, 20)
	if _, err := rand.Read(deviceID); err != nil {
		return "", fmt.Errorf("guest: generate device id: %w", err)
	}
	regID := make([]byte, 4)
	binary.BigEndian.PutUint32(regID, device.RegistrationID)
	keyID := make([]byte, 4)
	binary.BigEndian.PutUint32(keyID, device.SignedPreKey.KeyID)
	version := store.GetWAVersion()

	query := []string{
		param("mcc", "310"),
		param("mnc", "999"),
		param("lc", country),
		param("lg", language),
		param("tos_version", "1"),
		param("device_id", uuid.NewString()),
		param("access_session_id", sessionID),
		param("authkey", encodeKey(device.NoiseKey.Pub[:])),
		param("e_regid", encodeKey(regID)),
		param("e_keytype", encodeKey([]byte{5})),
		param("e_ident", encodeKey(device.IdentityKey.Pub[:])),
		param("e_skey_id", encodeKey(keyID[1:])),
		param("e_skey_val", encodeKey(device.SignedPreKey.Pub[:])),
		param("e_skey_sig", encodeKey(device.SignedPreKey.Signature[:])),
		param("os_name", browser.OSName),
		param("os_version", browser.OSVersion),
		param("browser", browser.Name),
		param("browser_version", browser.Version),
		param("web_build_version", "2."+strconv.FormatUint(uint64(version[1]), 10)+"."+strconv.FormatUint(uint64(version[2]), 10)),
		param("guest_type", "guest_calling"),
		param("invite_code", inviteCode),
		param("guest_name", name),
	}
	if req.PrivateInviteCode != "" {
		query = append(query, param("private_invite_code", req.PrivateInviteCode))
	}
	query = append(query,
		"id="+percentEncodeBytes(deviceID),
		"backup_token="+percentEncodeBytes(deviceID),
	)
	return strings.TrimRight(base, "/") + "/guest_reg?" + strings.Join(query, "&"), nil
}

// param encodes like the browser's encodeURIComponent: spaces become %20, not
// "+". QueryEscape already escapes a literal "+" as %2B, so the swap is safe.
func param(key, value string) string {
	return key + "=" + strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

// encodeKey is standard base64 with the URL-safe alphabet and padding kept,
// then query-escaped (the padding becomes %3D).
func encodeKey(value []byte) string {
	return strings.NewReplacer("+", "-", "/", "_").Replace(base64.StdEncoding.EncodeToString(value))
}

// percentEncodeBytes escapes every byte outside RFC 3986's unreserved set as
// %XX, one byte at a time, so bytes >= 0x80 stay single bytes.
func percentEncodeBytes(value []byte) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range value {
		if ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9') ||
			b == '-' || b == '_' || b == '.' || b == '~' {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&0x0f])
	}
	return out.String()
}

type registrationResponse struct {
	Status           string          `json:"status"`
	Reason           string          `json:"reason"`
	Param            string          `json:"param"`
	RetryAfter       json.RawMessage `json:"retry_after"`
	LID              string          `json:"lid"`
	EntAccessToken   string          `json:"ent_access_token"`
	EntCanonicalFBID string          `json:"ent_canonical_fbid"`
}

func parseRegistration(httpStatus int, body []byte, name string, browser Browser) (*Registration, error) {
	var parsed registrationResponse
	jsonErr := json.Unmarshal(body, &parsed)
	if httpStatus != http.StatusOK || jsonErr != nil || parsed.Status != "ok" {
		return nil, &Error{
			HTTPStatus: httpStatus, Status: parsed.Status, Reason: parsed.Reason,
			Param: parsed.Param, RetryAfter: strings.Trim(string(parsed.RetryAfter), `"`),
			Body: truncate(string(body), 512),
		}
	}
	user := strings.TrimSpace(parsed.LID)
	if at := strings.IndexByte(user, '@'); at >= 0 {
		user = user[:at]
	}
	if _, err := strconv.ParseUint(user, 10, 64); err != nil {
		return nil, fmt.Errorf("guest: registration returned an invalid lid %q", parsed.LID)
	}
	return &Registration{
		LID:            types.NewJID(user, types.HiddenUserServer),
		Name:           name,
		EntAccessToken: parsed.EntAccessToken, EntCanonicalFBID: parsed.EntCanonicalFBID,
		Browser: browser,
	}, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
