-- +goose Up
-- Ubuntu 26.04 is out and the seed's OS list stopped at 24.04. A published
-- version is immutable, so the pristine seed gets a new version instead of a
-- rewrite; existing servers stay on the version they were built with. The
-- "+" form allows later releases of the same distribution
-- (docs/processes/servers/template-authoring.md).
INSERT INTO servers_template_versions (template_id, number, notes, warnings, source, published_at, published_by)
SELECT t.id, 2, 'Supports Ubuntu 26.04 and later Ubuntu releases.', '[]', '{}',
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), 'system'
FROM servers_templates t
JOIN servers_template_versions v ON v.template_id = t.id AND v.number = 1
JOIN servers_template_files f ON f.version_id = v.id AND f.path = 'manifest.yaml'
WHERE t.slug = 'vless-xhttp'
  AND t.default_version = 1
  AND NOT EXISTS (SELECT 1 FROM servers_template_versions later
                  WHERE later.template_id = t.id AND later.number > 1)
  AND instr(CAST(f.content AS TEXT), 'os: [debian-12, debian-13, ubuntu-22.04, ubuntu-24.04]') > 0;

INSERT INTO servers_template_files (version_id, path, content)
SELECT v2.id, f.path,
       CAST(CASE WHEN f.path = 'manifest.yaml'
                 THEN replace(CAST(f.content AS TEXT),
                              'os: [debian-12, debian-13, ubuntu-22.04, ubuntu-24.04]',
                              'os: [debian-12+, ubuntu-22.04+]')
                 ELSE f.content END AS BLOB)
FROM servers_template_versions v1
JOIN servers_templates t ON t.id = v1.template_id
JOIN servers_template_files f ON f.version_id = v1.id
JOIN servers_template_versions v2 ON v2.template_id = t.id AND v2.number = 2
WHERE t.slug = 'vless-xhttp' AND v1.number = 1
  AND v2.published_by = 'system'
  AND v2.notes = 'Supports Ubuntu 26.04 and later Ubuntu releases.';

UPDATE servers_templates SET default_version = 2
WHERE slug = 'vless-xhttp' AND default_version = 1
  AND EXISTS (SELECT 1 FROM servers_template_versions v
              WHERE v.template_id = servers_templates.id AND v.number = 2
                AND v.published_by = 'system'
                AND v.notes = 'Supports Ubuntu 26.04 and later Ubuntu releases.');

-- +goose Down
-- The new version stays: new servers may already have been built from it.
SELECT 1;