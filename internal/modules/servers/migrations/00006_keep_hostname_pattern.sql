-- +goose Up
-- The hostname pattern has no default any more, because each install has its
-- own zone. An install that built servers under the old default keeps
-- building them in the same zone. This rebuilds its pattern from the newest
-- server's management hostname, with the server name swapped for
-- {country}-{number}. A pattern saved in Settings is a stored row already,
-- and it stays.
INSERT INTO settings (key, value, updated_at)
    SELECT 'servers.hostname_pattern',
           '{country}-{number}' || substr(management_hostname, length(name) + 1),
           strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
    FROM servers_servers
    WHERE substr(management_hostname, 1, length(name) + 1) = name || '.'
    ORDER BY id DESC
    LIMIT 1
ON CONFLICT (key) DO NOTHING;

-- +goose Down
-- The row stays: it is the pattern the install builds servers with, the same
-- value a member could have saved.
SELECT 1;
