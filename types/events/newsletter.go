// Copyright (c) 2026 Rajeh Taher
// Licensed under the MIT License. See LICENSE-MIT for details.

package events

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// UnmarshalJSON accepts either quoted or numeric decimal server positions.
// Parse the original bytes so large IDs never pass through a float64.
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
	if len(position) > 0 && position[0] == '"' {
		if err := json.Unmarshal(wire.Position, &position); err != nil {
			return err
		}
	}
	if len(position) == 0 {
		return fmt.Errorf("newsletter milestone has no message position")
	}
	for _, digit := range position {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("newsletter milestone has an invalid message position")
		}
	}
	id, err := strconv.ParseInt(position, 10, strconv.IntSize)
	if err != nil || id <= 0 {
		return fmt.Errorf("newsletter milestone message position is out of range")
	}
	decoded.MessageServerID = int(id)
	*evt = NewsletterMilestone(decoded)
	return nil
}
