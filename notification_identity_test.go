package whatsmeow

import (
	"context"
	"slices"
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type identityChangeStore struct {
	store.NoopStore
	lid             types.JID
	pn              types.JID
	identityDeletes []string
	sessionDeletes  []string
}

func (s *identityChangeStore) DeleteAllIdentities(_ context.Context, user string) error {
	s.identityDeletes = append(s.identityDeletes, user)
	return nil
}

func (s *identityChangeStore) DeleteAllSessions(_ context.Context, user string) error {
	s.sessionDeletes = append(s.sessionDeletes, user)
	return nil
}

func (s *identityChangeStore) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	if pn.User == s.pn.User {
		return s.lid, nil
	}
	return types.EmptyJID, nil
}

func (s *identityChangeStore) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	if lid.User == s.lid.User {
		return s.pn, nil
	}
	return types.EmptyJID, nil
}

func TestIdentityChangeDeletesPNAndLIDSignalState(t *testing.T) {
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)
	recorder := &identityChangeStore{pn: pn, lid: lid}
	client := NewClient(&store.Device{
		Identities:    recorder,
		Sessions:      recorder,
		LIDs:          recorder,
		PrivacyTokens: recorder,
	}, waLog.Noop)

	client.handleEncryptNotification(context.Background(), &waBinary.Node{
		Tag:     "notification",
		Attrs:   waBinary.Attrs{"from": pn},
		Content: []waBinary.Node{{Tag: "identity"}},
	})

	want := []string{pn.User, pn.User + "_128", lid.User + "_1", lid.User + "_129"}
	if !slices.Equal(recorder.identityDeletes, want) {
		t.Fatalf("identity deletes = %v, want %v", recorder.identityDeletes, want)
	}
	if !slices.Equal(recorder.sessionDeletes, want) {
		t.Fatalf("session deletes = %v, want %v", recorder.sessionDeletes, want)
	}
}

func TestIdentityChangeFromLIDDeletesMappedPNState(t *testing.T) {
	pn := types.NewJID("15551234567", types.DefaultUserServer)
	lid := types.NewJID("123456789012345", types.HiddenUserServer)
	recorder := &identityChangeStore{pn: pn, lid: lid}
	client := NewClient(&store.Device{
		Identities:    recorder,
		Sessions:      recorder,
		LIDs:          recorder,
		PrivacyTokens: recorder,
	}, waLog.Noop)

	client.handleEncryptNotification(context.Background(), &waBinary.Node{
		Tag:     "notification",
		Attrs:   waBinary.Attrs{"from": lid},
		Content: []waBinary.Node{{Tag: "identity"}},
	})

	want := []string{lid.User + "_1", lid.User + "_129", pn.User, pn.User + "_128"}
	if !slices.Equal(recorder.identityDeletes, want) {
		t.Fatalf("identity deletes = %v, want %v", recorder.identityDeletes, want)
	}
	if !slices.Equal(recorder.sessionDeletes, want) {
		t.Fatalf("session deletes = %v, want %v", recorder.sessionDeletes, want)
	}
}
