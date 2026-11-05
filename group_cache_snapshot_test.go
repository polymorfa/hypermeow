// Copyright (c) 2026 Polymorfa (Softium LLC)
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package whatsmeow

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/polymorfa/hypermeow/proto/waE2E"
	"github.com/polymorfa/hypermeow/types"
	"github.com/polymorfa/hypermeow/types/events"
)

func TestCachedGroupSnapshotIsolation(t *testing.T) {
	group := types.NewJID("120363000000001", types.GroupServer)
	admin := types.NewJID("100000000000001", types.HiddenUserServer)
	member := types.NewJID("100000000000002", types.HiddenUserServer)
	replacement := types.NewJID("100000000000003", types.HiddenUserServer)
	cli := &Client{groupCache: map[types.JID]*groupMetaCache{
		group: {ReadOnly: true, Members: []types.JID{admin, member}, Admins: []types.JID{admin}},
	}}
	snapshot, err := cli.getCachedGroupData(context.Background(), group)
	if err != nil {
		t.Fatal(err)
	}
	cli.updateGroupParticipantCache(&events.GroupInfo{
		JID: group, Leave: []types.JID{admin}, Promote: []types.JID{member},
		Announce: &types.GroupAnnounce{IsAnnounce: false},
	})
	if !snapshot.ReadOnly || !slices.Equal(snapshot.Members, []types.JID{admin, member}) || !slices.Equal(snapshot.Admins, []types.JID{admin}) {
		t.Fatal("notification mutated a previously returned group snapshot")
	}
	message := &waE2E.Message{}
	if !snapshot.CanSend(admin, message) || snapshot.CanSend(member, message) {
		t.Fatal("notification changed the snapshot's send permissions")
	}
	snapshot.Members[0] = replacement
	snapshot.Admins[0] = replacement
	current, err := cli.getCachedGroupData(context.Background(), group)
	if err != nil {
		t.Fatal(err)
	}
	if current.ReadOnly || !slices.Equal(current.Members, []types.JID{member}) || !slices.Equal(current.Admins, []types.JID{member}) {
		t.Fatal("caller mutated metadata owned by the group cache")
	}
}

func TestCachedGroupPermissionsConcurrentNotifications(t *testing.T) {
	group := types.NewJID("120363000000001", types.GroupServer)
	member := types.NewJID("100000000000001", types.HiddenUserServer)
	cli := &Client{groupCache: map[types.JID]*groupMetaCache{
		group: {ReadOnly: true, Members: []types.JID{member}},
	}}
	start := make(chan struct{})
	var updates sync.WaitGroup
	updates.Add(1)
	go func() {
		defer updates.Done()
		<-start
		for range 1000 {
			cli.updateGroupParticipantCache(&events.GroupInfo{
				JID: group, Promote: []types.JID{member},
				Announce: &types.GroupAnnounce{IsAnnounce: false},
			})
			cli.updateGroupParticipantCache(&events.GroupInfo{
				JID: group, Demote: []types.JID{member},
				Announce: &types.GroupAnnounce{IsAnnounce: true},
			})
		}
	}()
	close(start)
	allowed := 0
	for range 1000 {
		snapshot, err := cli.getCachedGroupData(context.Background(), group)
		if err != nil {
			updates.Wait()
			t.Fatal(err)
		}
		if snapshot.CanSend(member, &waE2E.Message{}) {
			allowed++
		}
	}
	updates.Wait()
	t.Logf("checked 1000 independent permission snapshots, %d allowed", allowed)
}
