-- +goose Up
-- A server's IP can change (docs/processes/servers/ip-change.md). Checks from
-- before the change are about the old IP and no longer count.
ALTER TABLE servers_servers ADD COLUMN ip_changed_at TEXT;

-- An IP a server used before a change and still answers on: Proxier keeps
-- proxy-testing it from home, so the server can be switched back once it
-- works again. One server at a time has an IP, as an IP or a previous one.
CREATE TABLE servers_previous_ips (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id       INTEGER NOT NULL REFERENCES servers_servers(id),
    ip              TEXT NOT NULL UNIQUE,
    since           TEXT NOT NULL,               -- when it stopped being the server's IP
    state           TEXT NOT NULL DEFAULT 'unknown' CHECK (state IN ('unknown','works','fails')),
    state_since     TEXT NOT NULL,
    candidate       TEXT CHECK (candidate IN ('works','fails')),
    candidate_count INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX servers_previous_ips_server ON servers_previous_ips(server_id);

-- One proxy test of a previous IP through every endpoint of its server.
CREATE TABLE servers_previous_ip_results (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    previous_ip_id INTEGER NOT NULL REFERENCES servers_previous_ips(id) ON DELETE CASCADE,
    at             TEXT NOT NULL,
    ok             INTEGER NOT NULL CHECK (ok IN (0,1)),
    class          TEXT NOT NULL DEFAULT '',     -- the first failing endpoint's: 'tcp-timeout', 'stalled', …
    inconclusive   INTEGER NOT NULL DEFAULT 0 CHECK (inconclusive IN (0,1)),
    detail         TEXT NOT NULL DEFAULT '{}'    -- JSON: per endpoint, timings or the error
) STRICT;
CREATE INDEX servers_previous_ip_results_latest ON servers_previous_ip_results(previous_ip_id, at);
CREATE INDEX servers_previous_ip_results_at ON servers_previous_ip_results(at);

-- +goose Down
DROP TABLE servers_previous_ip_results;
DROP TABLE servers_previous_ips;
ALTER TABLE servers_servers DROP COLUMN ip_changed_at;
