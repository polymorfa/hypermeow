-- v16 (compatible with v8+): Index PN-addressed sender keys for LID migration checks
CREATE INDEX whatsmeow_sender_keys_sender_idx ON whatsmeow_sender_keys (our_jid, sender_id);
