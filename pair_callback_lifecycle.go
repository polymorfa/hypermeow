package whatsmeow

import (
	"github.com/polymorfa/hypermeow/proto/waWa6"
	"github.com/polymorfa/hypermeow/types"
)

// Admission owns this decision through actual callback return/panic. Nil keeps
// its normal allow behavior. A seal during invocation rejects the late decision
// before the existing pairing persistence/confirmation continuation is entered.
func (cli *Client) prePairCallbackDecision(jid types.JID, platform, businessName string) (bool, error) {
	done, ok := cli.enterNative()
	if !ok {
		return false, ErrClientClosed
	}
	defer done()
	allowed := true
	if cli.PrePairCallback != nil {
		allowed = cli.PrePairCallback(jid, platform, businessName)
	}
	if cli.nativeClosed() {
		return false, ErrClientClosed
	}
	return allowed, nil
}

// Keep the original custom callback or built-in fallback and exact payload.
// Refusal/late sealing is an error, never an invented payload to marshal. This
// owns the synchronous invocation, not user-spawned callback/store descendants.
func (cli *Client) clientPayloadForHandshake() (*waWa6.ClientPayload, error) {
	done, ok := cli.enterNative()
	if !ok {
		return nil, ErrClientClosed
	}
	defer done()
	var payload *waWa6.ClientPayload
	if cli.GetClientPayload != nil {
		payload = cli.GetClientPayload()
	} else {
		payload = cli.Store.GetClientPayload()
	}
	if cli.nativeClosed() {
		return nil, ErrClientClosed
	}
	return payload, nil
}
