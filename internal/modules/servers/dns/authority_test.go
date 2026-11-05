package dns_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tikhonp/proxier/internal/modules/servers/dns"
	"golang.org/x/net/dns/dnsmessage"
)

// fakeClock moves only when the wait sleeps, so a 30-minute wait takes no time.
type fakeClock struct {
	mu    sync.Mutex
	start time.Time
	now   time.Time
}

func newFakeClock() *fakeClock {
	t := time.Date(2026, 11, 5, 17, 48, 25, 0, time.UTC)
	return &fakeClock{start: t, now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

// since is how long the wait has run.
func (c *fakeClock) since() time.Duration { return c.Now().Sub(c.start) }

var (
	gabe1 = dns.NameServer{Name: "gabe.ns.cloudflare.com", Addr: netip.MustParseAddrPort("108.162.193.114:53")}
	gabe2 = dns.NameServer{Name: "gabe.ns.cloudflare.com", Addr: netip.MustParseAddrPort("172.64.33.114:53")}
	tia   = dns.NameServer{Name: "tia.ns.cloudflare.com", Addr: netip.MustParseAddrPort("172.64.32.229:53")}
)

// waiterAt is the production waiter on a fake clock, for a Cloudflare zone
// (negative TTL 30 min) whose nameservers answer as ask says and whose
// resolvers as lookup says; both get the time since the start.
func waiterAt(c *fakeClock, ask func(at time.Duration, ns dns.NameServer) ([]netip.Addr, error),
	lookup func(at time.Duration, r dns.Resolver) ([]netip.Addr, error)) dns.Waiter {
	w := dns.NewWaiter()
	w.Resolvers = []dns.Resolver{{Name: "Cloudflare"}, {Name: "Google"}}
	w.Clock = c
	w.FindZone = func(_ context.Context, zone string) (dns.Zone, error) {
		if zone != waitZone {
			return dns.Zone{}, fmt.Errorf("asked about zone %q", zone)
		}
		return dns.Zone{Servers: []dns.NameServer{gabe1, gabe2, tia}, NegativeTTL: 30 * time.Minute}, nil
	}
	w.Ask = func(_ context.Context, ns dns.NameServer, name string) ([]netip.Addr, error) {
		if name != waitName {
			return nil, fmt.Errorf("asked about %q", name)
		}
		return ask(c.since(), ns)
	}
	w.Lookup = func(_ context.Context, r dns.Resolver, _ string) ([]netip.Addr, string, error) {
		addrs, err := lookup(c.since(), r)
		return addrs, "doh", err
	}
	return w
}

func ipAddrs(s ...string) []netip.Addr {
	var out []netip.Addr
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func served(at time.Duration, ns dns.NameServer) ([]netip.Addr, error) { return ipAddrs(waitIP), nil }
func seen(at time.Duration, r dns.Resolver) ([]netip.Addr, error)      { return ipAddrs(waitIP), nil }

// A resolver asked before the nameservers serve the record caches "no such
// name" for the zone's negative TTL: no public question goes out before then.
func TestNoPublicQuestionBeforeTheNameserversServeTheRecord(t *testing.T) {
	c := newFakeClock()
	var first time.Duration = -1
	w := waiterAt(c,
		func(at time.Duration, ns dns.NameServer) ([]netip.Addr, error) {
			// tia has it after 3 s, gabe's second address after 7 s
			if (ns == tia && at < 3*time.Second) || (ns == gabe2 && at < 7*time.Second) {
				return nil, nil
			}
			return ipAddrs(waitIP), nil
		},
		func(at time.Duration, r dns.Resolver) ([]netip.Addr, error) {
			if first < 0 {
				first = at
			}
			return ipAddrs(waitIP), nil
		})
	log := &memLog{}
	if err := w.Wait(t.Context(), waitZone, waitName, waitIP, log); err != nil {
		t.Fatalf("wait: %v\n%s", err, log.text())
	}
	// The nameservers are asked every 2 s: all of them have it at 8 s.
	if first != 8*time.Second {
		t.Errorf("the first public question went out at %s", first)
	}
	want := []string{
		"gabe.ns.cloudflare.com (172.64.33.114) answered nothing",
		"tia.ns.cloudflare.com (172.64.32.229) answered nothing",
		"tikhonnnnn.com's nameservers (gabe.ns.cloudflare.com, tia.ns.cloudflare.com) answer " + waitIP,
		"Cloudflare (doh) answered " + waitIP,
		"Google (doh) answered " + waitIP,
	}
	if got := log.text(); got != strings.Join(want, "\n") {
		t.Errorf("log:\n%s", got)
	}

	// Nameservers that never serve it: the wait ends at Timeout, without a
	// single public question, and says so.
	c = newFakeClock()
	asked := false
	w = waiterAt(c,
		func(time.Duration, dns.NameServer) ([]netip.Addr, error) { return ipAddrs("203.0.113.1"), nil },
		func(time.Duration, dns.Resolver) ([]netip.Addr, error) { asked = true; return nil, nil })
	err := w.Wait(t.Context(), waitZone, waitName, waitIP, nil)
	if !errors.Is(err, dns.ErrNotVisible) || !strings.Contains(err.Error(), "tikhonnnnn.com's own nameservers don't serve it: gabe.ns.cloudflare.com (108.162.193.114) answered 203.0.113.1") {
		t.Errorf("err = %v", err)
	}
	if asked || c.since() != 10*time.Minute {
		t.Errorf("asked the resolvers %v, gave up after %s", asked, c.since())
	}

	// A cancel ends the wait for the nameservers at once.
	ctx, cancel := context.WithCancel(t.Context())
	w = waiterAt(newFakeClock(),
		func(time.Duration, dns.NameServer) ([]netip.Addr, error) { cancel(); return nil, nil }, seen)
	if err := w.Wait(ctx, waitZone, waitName, waitIP, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}
}

// Nameservers that can't be asked, or whose answers someone else gives, don't
// fail the wait: it pauses Settle instead and then asks the resolvers.
func TestUnreachableNameserversFallBackToAPause(t *testing.T) {
	cases := []struct {
		name     string
		findZone func(context.Context, string) (dns.Zone, error)
		ask      func(time.Duration, dns.NameServer) ([]netip.Addr, error)
		log      string
	}{
		{"port 53 dropped", nil,
			func(time.Duration, dns.NameServer) ([]netip.Addr, error) {
				return nil, errors.New("UDP: i/o timeout; TCP: i/o timeout")
			},
			"Can't ask gabe.ns.cloudflare.com (108.162.193.114) about " + waitName + ": UDP: i/o timeout; TCP: i/o timeout; asking the public resolvers in 10 s instead"},
		{"port 53 intercepted", nil,
			func(time.Duration, dns.NameServer) ([]netip.Addr, error) { return nil, dns.ErrNotAuthoritative },
			"the answer is not authoritative: something on the way answered instead; asking the public resolvers in 10 s instead"},
		{"one address of several dropped", nil,
			func(_ time.Duration, ns dns.NameServer) ([]netip.Addr, error) {
				if ns == tia {
					return nil, errors.New("i/o timeout")
				}
				return ipAddrs(waitIP), nil
			},
			"Can't ask tia.ns.cloudflare.com (172.64.32.229)"},
		{"no nameservers found",
			func(context.Context, string) (dns.Zone, error) { return dns.Zone{}, errors.New("Cloudflare: HTTP 502") },
			nil,
			"Can't find tikhonnnnn.com's nameservers: Cloudflare: HTTP 502; asking the public resolvers in 10 s instead"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeClock()
			var first time.Duration = -1
			w := waiterAt(c, tc.ask, func(at time.Duration, _ dns.Resolver) ([]netip.Addr, error) {
				if first < 0 {
					first = at
				}
				return ipAddrs(waitIP), nil
			})
			if tc.findZone != nil {
				w.FindZone = tc.findZone
			}
			log := &memLog{}
			if err := w.Wait(t.Context(), waitZone, waitName, waitIP, log); err != nil {
				t.Fatalf("wait: %v", err)
			}
			if first != 10*time.Second {
				t.Errorf("the first public question went out at %s", first)
			}
			if !strings.Contains(log.text(), tc.log) {
				t.Errorf("log:\n%s", log.text())
			}
		})
	}

	// Without the zone's SOA, a cached negative answer gets Cloudflare's 30 min.
	c := newFakeClock()
	w := waiterAt(c, nil, func(at time.Duration, _ dns.Resolver) ([]netip.Addr, error) {
		if at < 20*time.Minute {
			return nil, nil
		}
		return ipAddrs(waitIP), nil
	})
	w.FindZone = func(context.Context, string) (dns.Zone, error) { return dns.Zone{}, errors.New("no NS records") }
	if err := w.Wait(t.Context(), waitZone, waitName, waitIP, nil); err != nil || c.since() != 20*time.Minute {
		t.Errorf("err %v after %s", err, c.since())
	}

	// No zone known at all: the same pause.
	c = newFakeClock()
	w = waiterAt(c, nil, seen)
	log := &memLog{}
	if err := w.Wait(t.Context(), "", waitName, waitIP, log); err != nil || c.since() != 10*time.Second ||
		!strings.Contains(log.text(), "zone is not known") {
		t.Errorf("err %v after %s\n%s", err, c.since(), log.text())
	}
}

// What blackbox saw: Cloudflare kept answering nothing past the 10-minute
// Timeout. A resolver that answers nothing while the others show the IP gets
// until its negative answer must have expired.
func TestCachedNegativeAnswerOutlastsTheTimeout(t *testing.T) {
	c := newFakeClock()
	w := waiterAt(c, served, func(at time.Duration, r dns.Resolver) ([]netip.Addr, error) {
		if r.Name == "Cloudflare" && at < 25*time.Minute {
			return nil, nil
		}
		return ipAddrs(waitIP), nil
	})
	log := &memLog{}
	if err := w.Wait(t.Context(), waitZone, waitName, waitIP, log); err != nil {
		t.Fatalf("wait: %v\n%s", err, log.text())
	}
	if c.since() != 25*time.Minute {
		t.Errorf("ended after %s", c.since())
	}
	warn := "WARN Cloudflare still answers nothing after 10 min: probably a negative answer cached before the record existed, which the zone lets resolvers keep for 30 min; waiting up to 21 min more"
	if got := log.text(); strings.Count(got, warn) != 1 || strings.Count(got, "\n") != 4 {
		t.Errorf("log:\n%s", got)
	}

	// Still nothing once the negative TTL and the margin have passed: the
	// error names the resolver and why it probably answers nothing.
	c = newFakeClock()
	w = waiterAt(c, served, func(_ time.Duration, r dns.Resolver) ([]netip.Addr, error) {
		if r.Name == "Cloudflare" {
			return nil, nil
		}
		return ipAddrs(waitIP), nil
	})
	err := w.Wait(t.Context(), waitZone, waitName, waitIP, nil)
	if !errors.Is(err, dns.ErrNotVisible) || !strings.Contains(err.Error(),
		"Cloudflare answered nothing (probably a cached negative answer: the zone lets resolvers keep one for 30 min); Google answered "+waitIP) {
		t.Errorf("err = %v", err)
	}
	if c.since() != 31*time.Minute {
		t.Errorf("gave up after %s", c.since())
	}

	// Another IP is not a negative answer: the wait ends at Timeout.
	for name, google := range map[string]func() ([]netip.Addr, error){
		"another IP": func() ([]netip.Addr, error) { return ipAddrs("203.0.113.1"), nil },
		"failing":    func() ([]netip.Addr, error) { return nil, errors.New("i/o timeout") },
	} {
		c = newFakeClock()
		w = waiterAt(c, served, func(_ time.Duration, r dns.Resolver) ([]netip.Addr, error) {
			if r.Name == "Google" {
				return google()
			}
			return nil, nil
		})
		if err := w.Wait(t.Context(), waitZone, waitName, waitIP, nil); !errors.Is(err, dns.ErrNotVisible) || c.since() != 10*time.Minute {
			t.Errorf("%s: %v after %s", name, err, c.since())
		}
	}

	// A zone that lets no negative answer be cached gets no more time.
	c = newFakeClock()
	w = waiterAt(c, served, func(time.Duration, dns.Resolver) ([]netip.Addr, error) { return nil, nil })
	w.FindZone = func(context.Context, string) (dns.Zone, error) { return dns.Zone{Servers: []dns.NameServer{tia}}, nil }
	if err := w.Wait(t.Context(), waitZone, waitName, waitIP, nil); !errors.Is(err, dns.ErrNotVisible) ||
		strings.Contains(err.Error(), "probably") || c.since() != 10*time.Minute {
		t.Errorf("%v after %s", err, c.since())
	}
}

// nameserver answers A questions for waitName on a local port, over UDP
// and TCP, with the AA flag unless aa is false.
func nameserver(t *testing.T, aa, udp bool) netip.AddrPort {
	t.Helper()
	answer := func(wire []byte) []byte {
		var q dnsmessage.Message
		if q.Unpack(wire) != nil || len(q.Questions) != 1 || q.RecursionDesired {
			return nil // a nameserver is asked without recursion
		}
		resp := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, Authoritative: aa}, Questions: q.Questions}
		if strings.TrimSuffix(q.Questions[0].Name.String(), ".") == waitName {
			resp.Answers = []dnsmessage.Resource{{
				Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
				Body:   &dnsmessage.AResource{A: netip.MustParseAddr(waitIP).As4()},
			}}
		} else {
			resp.RCode = dnsmessage.RCodeNameError
		}
		out, _ := resp.Pack()
		return out
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				var n [2]byte
				if _, err := io.ReadFull(c, n[:]); err != nil {
					return
				}
				body := make([]byte, binary.BigEndian.Uint16(n[:]))
				if _, err := io.ReadFull(c, body); err != nil {
					return
				}
				out := answer(body)
				_, _ = c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(out))), out...))
			}()
		}
	}()
	addr := netip.MustParseAddrPort(ln.Addr().String())
	if udp {
		pc, err := net.ListenPacket("udp", addr.String())
		if err != nil {
			t.Skipf("UDP port %d is taken: %v", addr.Port(), err)
		}
		t.Cleanup(func() { _ = pc.Close() })
		go func() {
			buf := make([]byte, 512)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				_, _ = pc.WriteTo(answer(buf[:n]), from)
			}
		}()
	}
	return addr
}

// The real questions to a nameserver: over UDP, over TCP when UDP gets no
// answer, and an answer without the AA flag counts as intercepted.
func TestAskTheNameserversOverPort53(t *testing.T) {
	cases := []struct {
		name    string
		aa, udp bool
		log     string
	}{
		{"UDP", true, true, "tikhonnnnn.com's nameservers (ns.local) answer " + waitIP},
		{"TCP when UDP is closed", true, false, "tikhonnnnn.com's nameservers (ns.local) answer " + waitIP},
		{"without the AA flag", false, true, "WARN Can't ask ns.local (127.0.0.1) about " + waitName + ": the answer is not authoritative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := nameserver(t, tc.aa, tc.udp)
			c := newFakeClock()
			w := waiterAt(c, nil, seen)
			w.Ask = nil // the real one
			w.FindZone = func(context.Context, string) (dns.Zone, error) {
				return dns.Zone{Servers: []dns.NameServer{{Name: "ns.local", Addr: addr}}}, nil
			}
			log := &memLog{}
			if err := w.Wait(t.Context(), waitZone, waitName, waitIP, log); err != nil {
				t.Fatalf("wait: %v", err)
			}
			if !strings.Contains(log.text(), tc.log) {
				t.Errorf("log:\n%s", log.text())
			}
		})
	}
}
