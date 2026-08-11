-- v17 (compatible with v8+): Index PN migration prefix lookups on PostgreSQL
-- Copyright (c) 2026 Rajeh Taher
--
-- Licensed under the MIT License. See LICENSE-MIT for details.

-- only: postgres
CREATE INDEX whatsmeow_identity_keys_their_pattern_idx ON whatsmeow_identity_keys (our_jid, their_id text_pattern_ops);
-- only: postgres
CREATE INDEX whatsmeow_sessions_their_pattern_idx ON whatsmeow_sessions (our_jid, their_id text_pattern_ops);
-- only: postgres
CREATE INDEX whatsmeow_sender_keys_sender_pattern_idx ON whatsmeow_sender_keys (our_jid, sender_id text_pattern_ops);
