-- v17: Persist the optional WhatsApp username alongside the LID-keyed contact.
ALTER TABLE whatsmeow_contacts ADD COLUMN username TEXT;
