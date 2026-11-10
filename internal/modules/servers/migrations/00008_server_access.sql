-- +goose Up
-- How Proxier reaches a server: the root password it is created with (then
-- the proxier user and its key), or an account whose authorized_keys already
-- holds Proxier's key (docs/processes/servers/server-provisioning.md).
ALTER TABLE servers_servers ADD COLUMN access_mode TEXT NOT NULL DEFAULT 'password'
    CHECK (access_mode IN ('password', 'key'));
ALTER TABLE servers_servers ADD COLUMN ssh_user TEXT NOT NULL DEFAULT 'proxier';

-- What provisioning does to the machine besides the stack: the full base
-- setup (SSH hardening, firewall, BBR, Docker) or the minimal one (Docker and
-- its prerequisites only).
ALTER TABLE servers_servers ADD COLUMN setup TEXT NOT NULL DEFAULT 'full'
    CHECK (setup IN ('full', 'minimal'));

-- +goose Down
ALTER TABLE servers_servers DROP COLUMN setup;
ALTER TABLE servers_servers DROP COLUMN ssh_user;
ALTER TABLE servers_servers DROP COLUMN access_mode;