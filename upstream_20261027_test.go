// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
)

func TestUpstreamGroupPermissionsRetainBoundedCache(t *testing.T) {
	cli, _, _ := newTestRemote(t)
	admin := types.NewJID("100000000000001", types.HiddenUserServer)
	member := types.NewJID("100000000000002", types.HiddenUserServer)
	var last types.JID
	for i := range maxGroupCacheEntries + 2 {
		last = types.NewJID(fmt.Sprintf("120363000000%03d", i), types.GroupServer)
		cli.cacheGroupInfo(&types.GroupInfo{
			JID:           last,
			GroupAnnounce: types.GroupAnnounce{IsAnnounce: true},
			Participants:  []types.GroupParticipant{{JID: admin, IsAdmin: true}, {JID: member}},
		}, true)
	}
	if len(cli.groupCache) != maxGroupCacheEntries {
		t.Fatalf("group cache grew to %d entries", len(cli.groupCache))
	}
	cached := cli.groupCache[last]
	if !cached.ReadOnly || !slices.Equal(cached.Admins, []types.JID{admin}) {
		t.Fatal("bounded insertion lost upstream permission metadata")
	}
	cli.updateGroupParticipantCache(&events.GroupInfo{
		JID: last, Promote: []types.JID{member}, Demote: []types.JID{admin},
		Announce: &types.GroupAnnounce{IsAnnounce: false},
	})
	if cached.ReadOnly || !slices.Equal(cached.Admins, []types.JID{member}) {
		t.Fatal("group permission changes were not applied to the bounded cache")
	}
	cli.updateGroupParticipantCache(&events.GroupInfo{JID: last, Leave: []types.JID{member}})
	if slices.Contains(cached.Members, member) || slices.Contains(cached.Admins, member) {
		t.Fatal("leaving participant retained group send permissions")
	}
}

func TestResolveLIDRetainsUpstreamUnregisteredRecipientError(t *testing.T) {
	cli := newTestShadow(t, &fakeShadowRelay{})
	cli.Store.LIDs = &store.NoopStore{}
	_, err := cli.ResolveLID(context.Background(), types.NewJID("15550000001", types.DefaultUserServer))
	if !errors.Is(err, ErrRecipientNotOnWhatsApp) {
		t.Fatalf("missing USync LID lost upstream recipient error: %v", err)
	}
}

func TestConcurrentUnifiedSessionChecksRespectInterval(t *testing.T) {
	cli, _, sent := newTestRemote(t)
	cli.UnifiedSessionInterval = time.Hour
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			<-start
			cli.sendUnifiedSession(true)
		})
	}
	close(start)
	workers.Wait()
	if tags := sent.tags(); !slices.Equal(tags, []string{"ib:"}) {
		t.Fatalf("concurrent interval checks sent %v", tags)
	}
	cli.sendUnifiedSession(false)
	if len(sent.tags()) != 2 {
		t.Fatal("explicit unified-session send was incorrectly suppressed")
	}
}

func TestParticipantHashInvalidationCoversForkRecipientServers(t *testing.T) {
	for _, server := range []string{
		types.DefaultUserServer, types.HiddenUserServer, types.BotServer,
		types.HostedServer, types.HostedLIDServer, types.MessengerServer, types.GroupServer,
	} {
		t.Run(server, func(t *testing.T) {
			cli, _, _ := newTestRemote(t)
			target := types.NewJID("100000000000001", server)
			other := types.NewJID("100000000000002", server)
			cli.groupCache = map[types.JID]*groupMetaCache{target: {}, other: {}}
			cli.userDevicesCache = map[types.JID]deviceCache{target: {}, other: {}}
			cli.invalidateParticipantHashCaches(target)
			if server == types.GroupServer {
				if _, found := cli.groupCache[target]; found {
					t.Fatal("participant hash mismatch retained the group route")
				}
			} else if _, found := cli.userDevicesCache[target]; found {
				t.Fatal("participant hash mismatch retained the recipient device list")
			}
			if _, found := cli.groupCache[other]; !found {
				t.Fatal("invalidation removed an unrelated group")
			}
			if _, found := cli.userDevicesCache[other]; !found {
				t.Fatal("invalidation removed an unrelated recipient")
			}
		})
	}
}
