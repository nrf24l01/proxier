package provision_test

import (
	"strings"
	"testing"

	"github.com/tikhonp/proxier/internal/modules/servers/serverstest"
	"github.com/tikhonp/proxier/internal/modules/servers/store"
)

// A typed hostname in none of the Cloudflare zones: the member creates its
// record by hand, and Create checks it first.
func TestHostnameOutsideTheZones(t *testing.T) {
	h := serverstest.NewHarness(t, serverstest.StubProxy())
	f := h.Form()
	f.Hostname = "vpn.example.org"

	// The live summary names the record to create, and asks no one.
	s := h.Mod.Provision.Summarize(bg, f, false)
	if len(s.Errors) != 0 || !s.ManualDNS || s.DNSRecord != "A vpn.example.org → 127.0.0.1" || s.DNSFound != "" || s.ManagementHostname != "vpn.example.org" {
		t.Fatalf("summary: %+v", s)
	}

	for _, c := range []struct {
		name   string
		record []string
		said   string
	}{
		{"no record", nil, "answered nothing"},
		{"elsewhere", []string{"198.51.100.1"}, "answered 198.51.100.1"},
		{"a second address", []string{"127.0.0.1", "198.51.100.1"}, "answered 127.0.0.1, 198.51.100.1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h.SetRecord("vpn.example.org", c.record...)
			_, err := h.Mod.Provision.Create(bg, f, "admin")
			fe := refused(t, err)
			if m := fe["dns"]; m.Key != "servers.err.dns_manual_missing" || !strings.Contains(m.Args["said"].(string), c.said) || m.Args["ip"] != "127.0.0.1" {
				t.Fatalf("errors: %+v", fe)
			}
			nothingCreated(t, h)
			// The Check button says the same.
			check := f
			check.CheckDNS = true
			if s := h.Mod.Provision.Summarize(bg, check, false); s.Errors["dns"].Key != "servers.err.dns_manual_missing" {
				t.Fatalf("checked summary: %+v", s)
			}
		})
	}

	h.SetRecord("vpn.example.org", "127.0.0.1")
	check := f
	check.CheckDNS = true
	if s := h.Mod.Provision.Summarize(bg, check, false); len(s.Errors) != 0 || !strings.Contains(s.DNSFound, "answered 127.0.0.1") {
		t.Fatalf("checked summary: %+v", s)
	}
	id := h.Create(f)
	h.Drain()
	srv := h.Server(id)
	if srv.State != "active" || !srv.ManualDNS || srv.ManagementHostname != "vpn.example.org" || srv.ProxyHostname != "vpn.example.org" {
		t.Fatalf("server: %+v", srv)
	}
	// Proxier wrote no record of its own.
	if recs, err := store.DNSRecords(bg, h.App.DB.R, id); err != nil || len(recs) != 0 || len(h.CF.Records("proxier.test")) != 0 {
		t.Fatalf("records %+v %v, cloudflare %+v", recs, err, h.CF.Records("proxier.test"))
	}
	if ev := h.Events("server.created"); len(ev) != 1 || ev[0].Payload["manual_dns"] != true || ev[0].Payload["hostname"] != "vpn.example.org" {
		t.Errorf("server.created %+v", ev)
	}
	if log := h.Log(h.Job(id).ID); !strings.Contains(log, "made by hand") {
		t.Errorf("log:\n%s", log)
	}
}

// Without Cloudflare, every typed hostname is made by hand; a pattern's name
// still needs Cloudflare.
func TestHostnameByHandWithoutCloudflare(t *testing.T) {
	h := serverstest.NewHarness(t)
	h.StopJobs()
	if err := h.App.Settings.Set(bg, "admin", "cloudflare", map[string]string{"cloudflare.api_token": ""}); err != nil {
		t.Fatal(err)
	}
	f := h.Form()
	if s := h.Mod.Provision.Summarize(bg, f, false); s.Errors["dns"].Key != "servers.err.no_cloudflare" {
		t.Fatalf("pattern summary: %+v", s.Errors)
	}
	for _, host := range []string{"vpn.example.org", "vpn.proxier.test"} {
		f.Hostname = host
		if s := h.Mod.Provision.Summarize(bg, f, false); len(s.Errors) != 0 || !s.ManualDNS {
			t.Fatalf("%s: summary %+v", host, s)
		}
	}
	h.SetRecord("vpn.proxier.test", "127.0.0.1")
	if srv := h.Server(h.Create(f)); !srv.ManualDNS {
		t.Fatalf("server %+v", srv)
	}
}

// A record deleted between Create and the DNS step fails the step with what
// to create.
func TestHandMadeRecordGoneBeforeTheDNSStep(t *testing.T) {
	h := serverstest.NewHarness(t, serverstest.StubProxy())
	h.StopJobs()
	h.SetRecord("vpn.example.org", "127.0.0.1")
	f := h.Form()
	f.Hostname = "vpn.example.org"
	id := h.Create(f)
	h.SetRecord("vpn.example.org")
	h.StartJobs()
	h.Drain()
	srv := h.Server(id)
	if srv.State != "failed" || srv.FailedStep != "dns" || !strings.Contains(srv.FailedError, "A vpn.example.org → 127.0.0.1") {
		t.Fatalf("server %s at %s: %s", srv.State, srv.FailedStep, srv.FailedError)
	}
}
