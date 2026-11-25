package whatsmeow

import (
	"context"

	"github.com/polymorfa/hypermeow/store"
	"github.com/polymorfa/hypermeow/types"
)

func (cli *Client) usesStatusSession(info *types.MessageInfo) bool {
	isStatusContext := info.Chat == types.StatusBroadcastJID || info.IsGroupStatus && info.MsgMetaInfo.SessionScope == "status"
	if !isStatusContext {
		return false
	}
	mode := types.StatusSessionReceiverEnabled
	if cli.GetStatusSessionMode != nil {
		mode = cli.GetStatusSessionMode()
	}
	return mode == types.StatusSessionForced || (mode == types.StatusSessionReceiverEnabled || mode == types.StatusSessionSenderEnabled) && info.MsgMetaInfo.SessionScope == "status"
}

func (cli *Client) statusSessionContext(ctx context.Context, info *types.MessageInfo) context.Context {
	if cli.usesStatusSession(info) {
		return store.WithStatusSession(ctx)
	}
	return ctx
}
