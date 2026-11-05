package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// NameServer is one address of one of a zone's nameservers.
type NameServer struct {
	Name string         // "gabe.ns.cloudflare.com"
	Addr netip.AddrPort // port 53
}

// Zone is what a wait needs to know of the zone a name is in.
type Zone struct {
	Servers []NameServer
	// NegativeTTL is how long a resolver may keep a "no such name" answer:
	// the SOA's MINIMUM, capped by the SOA record's own TTL (RFC 2308). Zero
	// lets it keep none.
	NegativeTTL time.Duration
}

// ErrNotAuthoritative: a nameserver's answer came without the AA flag, so
// something on the way answered in its place.
var ErrNotAuthoritative = errors.New("the answer is not authoritative: something on the way answered instead")

// askTimeout is for one question over one transport. Cloudflare's
// nameservers answer from home in about 0.2 s.
const askTimeout = 3 * time.Second

// findZone asks the public resolvers over DoH for the zone's nameservers and
// their IPv4 addresses (only the zone's apex and the nameservers' names, never
// the name waited for), then the nameservers for the zone's SOA.
func (w Waiter) findZone(ctx context.Context, zone string) (Zone, error) {
	var z Zone
	var hosts, errs []string
	var via Resolver
	for _, r := range w.Resolvers {
		ans, err := w.dohQuery(ctx, r, zone, dnsmessage.TypeNS)
		if err != nil {
			errs = append(errs, r.Name+": "+err.Error())
			continue
		}
		for _, rr := range ans.Answers {
			if ns, ok := rr.Body.(*dnsmessage.NSResource); ok {
				hosts = append(hosts, strings.TrimSuffix(strings.ToLower(ns.NS.String()), "."))
			}
		}
		if len(hosts) > 0 {
			via = r
			break
		}
		errs = append(errs, r.Name+": no NS records")
	}
	if len(hosts) == 0 {
		return z, errors.New(strings.Join(errs, "; "))
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		addrs, _, err := w.lookup(ctx, via, h)
		if err != nil {
			return z, fmt.Errorf("%s: %w", h, err)
		}
		for _, a := range addrs {
			z.Servers = append(z.Servers, NameServer{Name: h, Addr: netip.AddrPortFrom(a, 53)})
		}
	}
	if len(z.Servers) == 0 {
		return z, fmt.Errorf("%s has no IPv4 address", strings.Join(hosts, ", "))
	}
	ttl, err := soaNegativeTTL(ctx, z.Servers[0], zone)
	if err != nil {
		// Without the SOA the wait still works; the deadline may be longer.
		ttl = DefaultNegativeTTL
	}
	z.NegativeTTL = ttl
	return z, nil
}

func soaNegativeTTL(ctx context.Context, ns NameServer, zone string) (time.Duration, error) {
	ans, err := askAuthoritative(ctx, ns, zone, dnsmessage.TypeSOA)
	if err != nil {
		return 0, err
	}
	for _, rr := range ans.Answers {
		if soa, ok := rr.Body.(*dnsmessage.SOAResource); ok {
			return time.Duration(min(soa.MinTTL, rr.Header.TTL)) * time.Second, nil
		}
	}
	return 0, fmt.Errorf("%s has no SOA record", zone)
}

// ask asks a nameserver for name's A records, without recursion.
func ask(ctx context.Context, ns NameServer, name string) ([]netip.Addr, error) {
	ans, err := askAuthoritative(ctx, ns, name, dnsmessage.TypeA)
	if err != nil {
		return nil, err
	}
	return aRecords(ans), nil
}

func askAuthoritative(ctx context.Context, ns NameServer, name string, t dnsmessage.Type) (dnsmessage.Message, error) {
	q, err := question(name, t, false)
	if err != nil {
		return dnsmessage.Message{}, err
	}
	ans, err := exchange(ctx, ns.Addr, q)
	if err != nil {
		return ans, err
	}
	if !ans.Authoritative {
		return ans, ErrNotAuthoritative
	}
	switch ans.RCode {
	case dnsmessage.RCodeSuccess, dnsmessage.RCodeNameError:
		return ans, nil
	}
	return ans, fmt.Errorf("DNS %s", ans.RCode)
}

// exchange asks over UDP, and over TCP when UDP gets no answer or a
// truncated one.
func exchange(ctx context.Context, server netip.AddrPort, q dnsmessage.Message) (dnsmessage.Message, error) {
	q.ID = uint16(rand.Uint32())
	wire, err := q.Pack()
	if err != nil {
		return dnsmessage.Message{}, err
	}
	ans, uerr := exchangeOver(ctx, "udp", server, wire, q.ID)
	if uerr == nil && !ans.Truncated {
		return ans, nil
	}
	if ctx.Err() != nil {
		return ans, ctx.Err()
	}
	ans, terr := exchangeOver(ctx, "tcp", server, wire, q.ID)
	if terr != nil {
		if uerr == nil {
			return ans, fmt.Errorf("TCP: %w", terr)
		}
		return ans, fmt.Errorf("UDP: %v; TCP: %w", uerr, terr)
	}
	return ans, nil
}

func exchangeOver(ctx context.Context, network string, server netip.AddrPort, wire []byte, id uint16) (dnsmessage.Message, error) {
	var ans dnsmessage.Message
	c, err := (&net.Dialer{Timeout: askTimeout}).DialContext(ctx, network, server.String())
	if err != nil {
		return ans, err
	}
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	_ = c.SetDeadline(time.Now().Add(askTimeout))
	if network == "tcp" {
		framed := binary.BigEndian.AppendUint16(make([]byte, 0, 2+len(wire)), uint16(len(wire))) //nolint:gosec // a question is far below 64 KiB
		wire = append(framed, wire...)
	}
	if _, err := c.Write(wire); err != nil {
		return ans, err
	}
	for {
		var body []byte
		if network == "tcp" {
			var n [2]byte
			if _, err := io.ReadFull(c, n[:]); err != nil {
				return ans, err
			}
			body = make([]byte, binary.BigEndian.Uint16(n[:]))
			if _, err := io.ReadFull(c, body); err != nil {
				return ans, err
			}
		} else {
			buf := make([]byte, 4096)
			n, err := c.Read(buf)
			if err != nil {
				return ans, err
			}
			body = buf[:n]
		}
		// A stray datagram is skipped; on TCP the stream is the answer.
		if err := ans.Unpack(body); err == nil && ans.ID == id && ans.Response {
			return ans, nil
		}
		if network == "tcp" {
			return ans, errors.New("unreadable DNS answer")
		}
	}
}
