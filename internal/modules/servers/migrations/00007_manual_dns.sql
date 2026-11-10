-- +goose Up
-- A server whose hostname is in none of the Cloudflare zones has an A record
-- that a member made by hand at another DNS provider. Proxier checks that
-- record but never writes or deletes it.
ALTER TABLE servers_servers ADD COLUMN manual_dns INTEGER NOT NULL DEFAULT 0 CHECK (manual_dns IN (0,1));

-- +goose Down
ALTER TABLE servers_servers DROP COLUMN manual_dns;
