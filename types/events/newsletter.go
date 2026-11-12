package events

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// UnmarshalJSON accepts either quoted or numeric decimal server positions.
// Parse the original bytes so large IDs never pass through a float64.
//
// Channel-level milestones (for example follower counts) may arrive without a
// message: WhatsApp Web handles a null message and keys the notification with
// position 0 (WAWebNotificationsNewsletterMilestoneNotification.js). A missing,
// null or zero position therefore decodes to MessageServerID 0, meaning "no
// message". Malformed positions are still rejected.
func (evt *NewsletterMilestone) UnmarshalJSON(data []byte) error {
	type plain NewsletterMilestone
	var decoded plain
	wire := struct {
		*plain
		Position json.RawMessage `json:"message_server_id"`
	}{plain: &decoded}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	position := string(wire.Position)
	if position == "null" {
		position = ""
	}
	if len(position) > 0 && position[0] == '"' {
		if err := json.Unmarshal(wire.Position, &position); err != nil {
			return err
		}
	}
	if len(wire.Position) == 0 || string(wire.Position) == "null" {
		decoded.MessageServerID = 0
		*evt = NewsletterMilestone(decoded)
		return nil
	}
	if len(position) == 0 {
		return fmt.Errorf("newsletter milestone has an empty message position")
	}
	for _, digit := range position {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("newsletter milestone has an invalid message position")
		}
	}
	id, err := strconv.ParseInt(position, 10, strconv.IntSize)
	if err != nil || id < 0 {
		return fmt.Errorf("newsletter milestone message position is out of range")
	}
	decoded.MessageServerID = int(id)
	*evt = NewsletterMilestone(decoded)
	return nil
}
