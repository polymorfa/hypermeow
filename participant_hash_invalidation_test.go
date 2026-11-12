package whatsmeow

import (
	"testing"

	"github.com/polymorfa/hypermeow/types"
)

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
