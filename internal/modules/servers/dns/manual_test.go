package dns_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/tikhonp/proxier/internal/modules/servers/dns"
)

// manualWaiter's world: example.org is a zone with two nameservers, which
// serve records; asked holds every name the zone's search and the public
// resolvers were asked about.
type manualWorld struct {
	records map[string][]netip.Addr
	nsDown  bool
	asked   []string // "NS example.org", "ns1 vpn.example.org", "Cloudflare vpn.example.org"
}

func (m *manualWorld) waiter() dns.Waiter {
	return dns.Waiter{
		Resolvers: []dns.Resolver{{Name: "Cloudflare"}, {Name: "Google"}},
		FindZone: func(_ context.Context, zone string) (dns.Zone, error) {
			m.asked = append(m.asked, "NS "+zone)
			if zone != "example.org" {
				return dns.Zone{}, errors.New("no NS records")
			}
			return dns.Zone{Servers: []dns.NameServer{
				{Name: "ns1", Addr: netip.MustParseAddrPort("192.0.2.1:53")},
				{Name: "ns2", Addr: netip.MustParseAddrPort("192.0.2.2:53")},
			}}, nil
		},
		Ask: func(_ context.Context, ns dns.NameServer, name string) ([]netip.Addr, error) {
			m.asked = append(m.asked, ns.Name+" "+name)
			if m.nsDown {
				return nil, errors.New("i/o timeout")
			}
			return m.records[name], nil
		},
		Lookup: func(_ context.Context, r dns.Resolver, name string) ([]netip.Addr, string, error) {
			m.asked = append(m.asked, r.Name+" "+name)
			return m.records[name], "doh", nil
		},
	}
}

func addrs(s ...string) []netip.Addr {
	var out []netip.Addr
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func TestCheckRecordMadeByHand(t *testing.T) {
	ctx := context.Background()
	const ip = "203.0.113.10"
	for _, c := range []struct {
		name    string
		records []netip.Addr
		ok      bool
		said    string
	}{
		{"in place", addrs(ip), true, "ns1 answered 203.0.113.10; ns2 answered 203.0.113.10"},
		{"missing", nil, false, "ns1 answered nothing"},
		{"elsewhere", addrs("198.51.100.1"), false, "answered 198.51.100.1"},
		// A second address sends some clients to another machine.
		{"two addresses", addrs(ip, "198.51.100.1"), false, "answered 198.51.100.1, 203.0.113.10"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &manualWorld{records: map[string][]netip.Addr{"a.b.example.org": c.records}}
			f, err := m.waiter().Check(ctx, "a.b.example.org", ip)
			if err != nil {
				t.Fatal(err)
			}
			if f.OK != c.ok || f.Zone != "example.org" || !strings.Contains(f.Said, c.said) {
				t.Fatalf("got %+v, want ok=%v in example.org saying %q", f, c.ok, c.said)
			}
			// Only the nameservers were asked about the name, and the zone
			// was searched for from the nearest parent up.
			want := []string{"NS b.example.org", "NS example.org", "ns1 a.b.example.org", "ns2 a.b.example.org"}
			if !slices.Equal(m.asked, want) {
				t.Fatalf("asked %q, want %q", m.asked, want)
			}
		})
	}
}

func TestCheckRecordAtTheApex(t *testing.T) {
	m := &manualWorld{records: map[string][]netip.Addr{"example.org": addrs("203.0.113.10")}}
	f, err := m.waiter().Check(context.Background(), "example.org", "203.0.113.10")
	if err != nil || !f.OK || f.Zone != "example.org" {
		t.Fatalf("got %+v %v", f, err)
	}
}

func TestCheckFallsBackToPublicResolvers(t *testing.T) {
	ctx := context.Background()
	// The nameservers don't answer (a home router dropping port 53).
	m := &manualWorld{nsDown: true, records: map[string][]netip.Addr{"vpn.example.org": addrs("203.0.113.10")}}
	f, err := m.waiter().Check(ctx, "vpn.example.org", "203.0.113.10")
	if err != nil || !f.OK || !strings.Contains(f.Said, "Google answered 203.0.113.10") {
		t.Fatalf("got %+v %v", f, err)
	}
	// No zone is found at all: the public resolvers answer.
	m = &manualWorld{records: map[string][]netip.Addr{"vpn.other.net": addrs("198.51.100.1")}}
	f, err = m.waiter().Check(ctx, "vpn.other.net", "203.0.113.10")
	if err != nil || f.OK || f.Zone != "" || !strings.Contains(f.Said, "Cloudflare answered 198.51.100.1") {
		t.Fatalf("got %+v %v", f, err)
	}
}

func TestApex(t *testing.T) {
	m := &manualWorld{}
	w := m.waiter()
	if z := w.Apex(context.Background(), "vpn.example.org"); z != "example.org" {
		t.Fatalf("apex %q", z)
	}
	if z := w.Apex(context.Background(), "vpn.other.net"); z != "" {
		t.Fatalf("apex of a name with no zone %q", z)
	}
	if slices.Contains(m.asked, "NS vpn.example.org") {
		t.Fatalf("asked about the name itself: %q", m.asked)
	}
}
