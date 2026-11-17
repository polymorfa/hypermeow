-- v21 (compatible with v8+): Retain legacy group publication receipt classification without plaintext
ALTER TABLE whatsmeow_event_buffer ADD COLUMN group_status BOOLEAN;
