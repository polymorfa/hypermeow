// Copyright (c) 2021 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package types

import (
	"fmt"
	"time"
)

type AddressingMode string

const (
	AddressingModePN  AddressingMode = "pn"
	AddressingModeLID AddressingMode = "lid"
)

type BroadcastRecipient struct {
	LID JID
	PN  JID
}

// MessageSource contains basic sender and chat information about a message.
type MessageSource struct {
	Chat     JID  // The chat where the message was sent.
	Sender   JID  // The user who sent the message.
	IsFromMe bool // Whether the message was sent by the current user instead of someone else.
	IsGroup  bool // Whether the chat is a group chat or broadcast list.

	AddressingMode AddressingMode // The addressing mode of the message (phone number or LID)
	SenderAlt      JID            // The alternative address of the user who sent the message
	RecipientAlt   JID            // The alternative address of the recipient of the message for DMs.

	// When sending a read receipt to a broadcast list message, the Chat is the broadcast list
	// and Sender is you, so this field contains the recipient of the read receipt.
	BroadcastListOwner  JID
	BroadcastRecipients []BroadcastRecipient
}

// IsIncomingBroadcast returns true if the message was sent to a broadcast list instead of directly to the user.
//
// If this is true, it means the message shows up in the direct chat with the Sender.
func (ms *MessageSource) IsIncomingBroadcast() bool {
	return (!ms.IsFromMe || !ms.BroadcastListOwner.IsEmpty()) && ms.Chat.IsBroadcastList()
}

// DeviceSentMeta contains metadata from messages sent by another one of the user's own devices.
type DeviceSentMeta struct {
	DestinationJID string // The destination user. This should match the MessageInfo.Recipient field.
	Phash          string
}

type EditAttribute string

const (
	EditAttributeEmpty        EditAttribute = ""
	EditAttributeMessageEdit  EditAttribute = "1"
	EditAttributePinInChat    EditAttribute = "2"
	EditAttributeAdminEdit    EditAttribute = "3" // only used in newsletters
	EditAttributeSenderRevoke EditAttribute = "7"
	EditAttributeAdminRevoke  EditAttribute = "8"
)

type BotEditType string

const (
	EditTypeFirst BotEditType = "first"
	EditTypeInner BotEditType = "inner"
	EditTypeLast  BotEditType = "last"
	EditTypeFull  BotEditType = "full"
)

type MsgBotInfo struct {
	EditType              BotEditType
	EditTargetID          MessageID
	EditSenderTimestampMS time.Time

	// For messages sent to bots
	ClientThreadID string
}

// MsgMetaInfo targets <meta>
type MsgMetaInfo struct {
	// Bot things
	TargetID     MessageID
	TargetSender JID
	TargetChat   JID

	SessionScope string // Explicit meta session_scope, when present.

	DeprecatedLIDSession *bool

	ThreadMessageID        MessageID
	ThreadMessageSenderJID JID
}

// StatusKind identifies a status publication, not a reply or mention notification.
type StatusKind string

const (
	StatusKindPersonal StatusKind = "personal"
	StatusKindGroup    StatusKind = "group"
	StatusKindChannel  StatusKind = "channel"
)

// StatusSessionMode matches the served status_infra_1_1_session_split states.
type StatusSessionMode int

const (
	StatusSessionDisabled StatusSessionMode = iota
	StatusSessionReceiverEnabled
	StatusSessionSenderEnabled
	StatusSessionForced
)

// MessageInfo contains metadata about an incoming message.
type MessageInfo struct {
	MessageSource
	ID        MessageID
	ServerID  MessageServerID
	Type      string
	PushName  string
	Timestamp time.Time
	Category  string
	Multicast bool
	MediaType string
	Edit      EditAttribute

	MsgBotInfo  MsgBotInfo
	MsgMetaInfo MsgMetaInfo

	IsStatusStanza     bool // The original transport tag was status, not message.
	IsGroupStatus      bool
	IsNewsletterStatus bool

	VerifiedName *VerifiedName
	// BizPrivacyMode is the privacy mode from <biz actual_actors host_storage
	// privacy_mode_ts/> on a message from a business. It is nil when the node or
	// any attribute is missing, an enum value is unknown, or the sender is a bot.
	BizPrivacyMode *BusinessPrivacyMode
	DeviceSentMeta *DeviceSentMeta // Metadata for direct messages sent from another one of the user's own devices.
}

// SourceString returns a log-friendly representation of who sent the message and where.
func (ms *MessageSource) SourceString() string {
	if ms.Sender != ms.Chat {
		return fmt.Sprintf("%s in %s", ms.Sender, ms.Chat)
	} else {
		return ms.Chat.String()
	}
}

// StatusKind returns the publication audience, or empty for an ordinary message.
// Quoted status content and status mention notifications do not change the audience.
func (info *MessageInfo) StatusKind() StatusKind {
	if info.IsNewsletterStatus {
		return StatusKindChannel
	}
	if info.IsGroupStatus {
		return StatusKindGroup
	}
	if info.Chat == StatusBroadcastJID {
		return StatusKindPersonal
	}
	return ""
}
