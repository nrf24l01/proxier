package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/tikhonp/proxier/internal/platform/db"
	"github.com/tikhonp/proxier/internal/platform/db/dbtest"
)

// The hostname pattern lost its default; an install that built servers under
// it keeps it, rebuilt from the newest server's management hostname.
func TestKeepHostnamePattern(t *testing.T) {
	ctx := context.Background()
	server := func(t *testing.T, d *db.DB, id int, country, name, host string) {
		t.Helper()
		mustExec(t, d, `INSERT INTO servers_servers (id, country, number, name, ip, management_hostname, proxy_hostname, state, template_id, template_version, health, health_since, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'retired', 1, 1, NULL, NULL, ?)`, id, country, id, name, "203.0.113."+name[len(name)-1:], host, host, now)
	}
	pattern := func(t *testing.T, d *db.DB) string {
		t.Helper()
		var v string
		err := d.R.GetContext(ctx, &v, `SELECT value FROM settings WHERE key = 'servers.hostname_pattern'`)
		if errors.Is(err, sql.ErrNoRows) {
			return "(none)"
		}
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		name  string
		saved string // a pattern saved in Settings before, "" for none
		hosts [][3]string
		want  string
	}{
		{name: "no servers", want: "(none)"},
		{name: "the old default", hosts: [][3]string{{"NL", "ams-1", "ams-1.hosts.example.com"}, {"NL", "nl-2", "nl-2.hosts.example.com"}},
			want: "{country}-{number}.hosts.example.com"},
		{name: "the newest server wins", hosts: [][3]string{{"NL", "nl-1", "nl-1.old.example.org"}, {"DE", "de-2", "de-2.px.example.net"}},
			want: "{country}-{number}.px.example.net"},
		{name: "a name not at the front says nothing", hosts: [][3]string{{"NL", "nl-1", "proxy1.example.com"}}, want: "(none)"},
		{name: "a saved pattern is kept", saved: "{country}{number}.px.example.org", hosts: [][3]string{{"NL", "nl-1", "nl1.px.example.org"}},
			want: "{country}{number}.px.example.org"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := dbtest.OpenEmpty(t)
			if err := db.MigrateUp(ctx, d, dbtest.Discard, dbtest.Platform, upTo{5}); err != nil {
				t.Fatal(err)
			}
			mustExec(t, d, `INSERT INTO servers_templates (id, slug, name, created_at) VALUES (1, 't', 'T', ?)`, now)
			for i, h := range c.hosts {
				server(t, d, i+1, h[0], h[1], h[2])
			}
			if c.saved != "" {
				mustExec(t, d, `INSERT INTO settings (key, value, updated_at) VALUES ('servers.hostname_pattern', ?, ?)`, c.saved, now)
			}
			if err := db.MigrateUp(ctx, d, dbtest.Discard, upTo{6}); err != nil {
				t.Fatal(err)
			}
			if got := pattern(t, d); got != c.want {
				t.Fatalf("pattern %q, want %q", got, c.want)
			}
			// down keeps the row: it is the pattern the install builds with
			if err := db.MigrateDown(ctx, d, dbtest.Discard, upTo{6}); err != nil {
				t.Fatal(err)
			}
			if got := pattern(t, d); got != c.want {
				t.Fatalf("after down: pattern %q, want %q", got, c.want)
			}
		})
	}
}
