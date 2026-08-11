-- v18 (compatible with v8+): Persist the optional WhatsApp username alongside the LID-keyed contact.
-- Copyright (c) 2026 Rajeh Taher
--
-- Licensed under the MIT License. See LICENSE-MIT for details.

ALTER TABLE whatsmeow_contacts ADD COLUMN username TEXT;
