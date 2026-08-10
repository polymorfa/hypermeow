package whatsmeow

import (
	"context"
	"encoding/json"
	"fmt"

	"go.mau.fi/whatsmeow/mex"
	"go.mau.fi/whatsmeow/types"
)

type deleteNewsletterVariables struct {
	NewsletterID string `json:"newsletter_id"`
}

func buildDeleteNewsletterVariables(jid types.JID) (deleteNewsletterVariables, error) {
	if jid.IsEmpty() || jid.User == "" || jid.Server != types.NewsletterServer {
		return deleteNewsletterVariables{}, fmt.Errorf("newsletter JID must use the newsletter server")
	}
	if len(jid.User) > 256 {
		return deleteNewsletterVariables{}, fmt.Errorf("newsletter JID user exceeds 256 bytes")
	}
	for _, character := range jid.User {
		if character < '0' || character > '9' {
			return deleteNewsletterVariables{}, fmt.Errorf("newsletter JID user must be numeric")
		}
	}
	if jid.RawAgent != 0 || jid.Device != 0 || jid.Integrator != 0 {
		return deleteNewsletterVariables{}, fmt.Errorf("newsletter JID must not identify a device")
	}
	return deleteNewsletterVariables{NewsletterID: jid.String()}, nil
}

func decodeDeleteNewsletterResponse(data json.RawMessage, requested types.JID) error {
	var response struct {
		Deleted *struct {
			ID    string `json:"id"`
			State *struct {
				Type string `json:"type"`
			} `json:"state"`
		} `json:"xwa2_newsletter_delete_v2"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("decode newsletter deletion response: %w", err)
	}
	if response.Deleted == nil {
		return fmt.Errorf("newsletter deletion response is missing xwa2_newsletter_delete_v2")
	}
	if response.Deleted.ID != requested.String() {
		return fmt.Errorf("newsletter deletion response ID %q does not match %q", response.Deleted.ID, requested)
	}
	if response.Deleted.State == nil || response.Deleted.State.Type != "DELETED" {
		return fmt.Errorf("newsletter deletion response did not confirm DELETED state")
	}
	return nil
}

// DeleteNewsletter permanently deletes a WhatsApp channel owned by the client.
func (cli *Client) DeleteNewsletter(ctx context.Context, jid types.JID) error {
	if cli == nil {
		return ErrClientIsNil
	}
	variables, err := buildDeleteNewsletterVariables(jid)
	if err != nil {
		return err
	}
	binding, ok := mex.Lookup(mex.DeleteNewsletter)
	if !ok {
		return fmt.Errorf("missing DeleteNewsletter MEX binding")
	}
	data, err := cli.sendMexIQ(ctx, binding.DocumentID, variables)
	if err != nil {
		return fmt.Errorf("delete newsletter: %w", err)
	}
	return decodeDeleteNewsletterResponse(data, jid)
}
