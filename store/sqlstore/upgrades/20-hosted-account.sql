-- v20 (compatible with v8+): Remember whether the companion is paired to a hosted (coexistence) account
ALTER TABLE whatsmeow_device ADD COLUMN is_hosted BOOLEAN NOT NULL DEFAULT false;
