package dns

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Resolver is a public resolver to ask.
type Resolver struct {
	Name string // "Cloudflare", "Google"
	DoH  string // "https://1.1.1.1/dns-query"
	UDP  string // "1.1.1.1:53": the fallback
}

// DefaultResolvers are the two Proxier waits for. Proxier sits behind a home
// router that may intercept port 53, so they are asked over DNS-over-HTTPS at
// IP-addressed URLs (no DNS needed to find them).
var DefaultResolvers = []Resolver{
	{Name: "Cloudflare", DoH: "https://1.1.1.1/dns-query", UDP: "1.1.1.1:53"},
	{Name: "Google", DoH: "https://8.8.8.8/dns-query", UDP: "8.8.8.8:53"},
}

// Log is where the wait reports; the job's logger satisfies it.
type Log interface {
	Info(format string, args ...any)
	Warn(format string, args ...any)
}

type nopLog struct{}

func (nopLog) Info(string, ...any) {}
func (nopLog) Warn(string, ...any) {}

// Clock is the time a wait runs on; tests pass a fake one.
type Clock interface {
	Now() time.Time
	// Sleep returns the context's error at once when it is cancelled.
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// DefaultNegativeTTL stands in for the zone's negative TTL when its SOA can't
// be read: Cloudflare's SOA MINIMUM, 1800 s.
const DefaultNegativeTTL = 30 * time.Minute

// Waiter waits until the zone's own nameservers serve a name's IP, then until
// every public resolver answers it.
//
// A resolver asked before the nameservers serve the record gets "no such
// name" and may keep that answer for the zone's negative TTL (30 min at
// Cloudflare), longer than Timeout. So the public resolvers are asked only
// once the nameservers have the record, and a resolver that still answers
// nothing at Timeout gets until its negative answer must have expired.
type Waiter struct {
	Resolvers []Resolver
	Every     time.Duration // between rounds of the public resolvers (10 s)
	Timeout   time.Duration // in all (10 min), unless a negative answer may still be cached
	AuthEvery time.Duration // between rounds of the zone's nameservers (2 s)
	// Settle is the pause before the first public round when the zone's
	// nameservers can't be asked (10 s): long enough for Cloudflare to
	// publish a new record, so the first questions don't meet its absence.
	Settle time.Duration
	Margin time.Duration // added to the negative TTL (1 min)
	HTTP   *http.Client
	Clock  Clock // nil is the real one

	// Lookup replaces both transports in tests; transport is "doh" or "udp".
	Lookup func(ctx context.Context, r Resolver, name string) (addrs []netip.Addr, transport string, err error)
	// FindZone replaces finding the zone's nameservers and negative TTL in tests.
	FindZone func(ctx context.Context, zone string) (Zone, error)
	// Ask replaces a question to one of the zone's nameservers in tests: the
	// name's A records, ErrNotAuthoritative, or why there was no answer.
	Ask func(ctx context.Context, ns NameServer, name string) ([]netip.Addr, error)
}

// NewWaiter is the production waiter.
func NewWaiter() Waiter {
	return Waiter{Resolvers: DefaultResolvers, Every: 10 * time.Second, Timeout: 10 * time.Minute,
		AuthEvery: 2 * time.Second, Settle: 10 * time.Second, Margin: time.Minute,
		HTTP: &http.Client{Timeout: 8 * time.Second}}
}

func (w Waiter) defaults() Waiter {
	if w.Every <= 0 {
		w.Every = 10 * time.Second
	}
	if w.Timeout <= 0 {
		w.Timeout = 10 * time.Minute
	}
	if w.AuthEvery <= 0 {
		w.AuthEvery = 2 * time.Second
	}
	if w.Settle <= 0 {
		w.Settle = 10 * time.Second
	}
	if w.Clock == nil {
		w.Clock = realClock{}
	}
	if w.Lookup == nil {
		w.Lookup = w.lookup
	}
	if w.FindZone == nil {
		w.FindZone = w.findZone
	}
	if w.Ask == nil {
		w.Ask = ask
	}
	return w
}

// Wait returns nil when every resolver answers name (in zone) with ip,
// ErrNotVisible (naming what each answered) once it gives up, or the
// context's error at once when it is cancelled.
func (w Waiter) Wait(ctx context.Context, zone, name, ip string, log Log) error {
	if log == nil {
		log = nopLog{}
	}
	want, err := netip.ParseAddr(ip)
	if err != nil {
		return fmt.Errorf("dns: %q is not an IP address", ip)
	}
	w = w.defaults()
	deadline := w.Clock.Now().Add(w.Timeout)
	negTTL, err := w.waitAuthoritative(ctx, zone, name, want, deadline, log)
	if err != nil {
		return err
	}
	// A negative answer a resolver holds now was cached before the record was
	// served, so it is gone by then.
	expired := w.Clock.Now().Add(negTTL + w.Margin)
	if negTTL <= 0 {
		expired = time.Time{}
	}
	extended := false

	// last is what each resolver last said, and over which transport; empty,
	// whether that was "nothing".
	last, lastVia, empty := map[string]string{}, map[string]string{}, map[string]bool{}
	for {
		ok := true
		for _, r := range w.Resolvers {
			addrs, transport, err := w.Lookup(ctx, r, name)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var said string
			empty[r.Name] = false
			switch {
			case err != nil:
				said = "failed: " + err.Error()
				ok = false
			case contains(addrs, want):
				said = "answered " + want.String()
			default:
				said = "answered " + describe(addrs)
				empty[r.Name] = len(addrs) == 0
				ok = false
			}
			// A line when the answer changes, so a long wait stays readable.
			if last[r.Name] != said || lastVia[r.Name] != transport {
				last[r.Name], lastVia[r.Name] = said, transport
				log.Info("%s (%s) %s", r.Name, transport, said)
			}
		}
		if ok {
			return nil
		}
		now := w.Clock.Now()
		if !now.Before(deadline) {
			stale := w.staleNegative(last, empty, want)
			if extended || len(stale) == 0 || !now.Before(expired) {
				return w.notVisible(last, empty, negTTL)
			}
			extended, deadline = true, expired
			log.Warn("%s still %s nothing after %s: probably a negative answer cached before the record existed, which the zone lets resolvers keep for %s; waiting up to %s more",
				strings.Join(stale, " and "), plural(len(stale), "answers", "answer"), human(w.Timeout), human(negTTL), human(expired.Sub(now)))
		}
		if err := w.Clock.Sleep(ctx, min(w.Every, deadline.Sub(now))); err != nil {
			return err
		}
	}
}

// staleNegative names the resolvers that answer nothing, when every other
// one answers the IP: only then does waiting out the negative TTL help.
func (w Waiter) staleNegative(last map[string]string, empty map[string]bool, want netip.Addr) []string {
	var names []string
	for _, r := range w.Resolvers {
		switch {
		case empty[r.Name]:
			names = append(names, r.Name)
		case last[r.Name] != "answered "+want.String():
			return nil
		}
	}
	return names
}

func (w Waiter) notVisible(last map[string]string, empty map[string]bool, negTTL time.Duration) error {
	var parts []string
	for _, r := range w.Resolvers {
		said := last[r.Name]
		if said == "" {
			said = "gave no answer"
		}
		if empty[r.Name] && negTTL > 0 {
			said += fmt.Sprintf(" (probably a cached negative answer: the zone lets resolvers keep one for %s)", human(negTTL))
		}
		parts = append(parts, r.Name+" "+said)
	}
	return fmt.Errorf("%w: %s", ErrNotVisible, strings.Join(parts, "; "))
}

// waitAuthoritative waits until every address of every nameserver of zone
// answers name with want, and returns the zone's negative TTL. When the
// nameservers can't be asked, or something else answers for them, it waits
// Settle instead.
func (w Waiter) waitAuthoritative(ctx context.Context, zone, name string, want netip.Addr, deadline time.Time, log Log) (time.Duration, error) {
	settle := func(why string) (time.Duration, error) {
		log.Warn("%s; asking the public resolvers in %s instead", why, human(w.Settle))
		return DefaultNegativeTTL, w.Clock.Sleep(ctx, w.Settle)
	}
	if zone == "" {
		return settle("The name's zone is not known, so its nameservers can't be asked")
	}
	z, err := w.FindZone(ctx, zone)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if err == nil && len(z.Servers) == 0 {
		err = errors.New("it has no nameservers")
	}
	if err != nil {
		return settle(fmt.Sprintf("Can't find %s's nameservers: %v", zone, err))
	}
	var names []string
	for _, ns := range z.Servers {
		if !slices.Contains(names, ns.Name) {
			names = append(names, ns.Name)
		}
	}
	last := map[string]string{}
	for {
		ok := true
		said := map[string]string{}
		for _, ns := range z.Servers {
			addrs, err := w.Ask(ctx, ns, name)
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if err != nil {
				_, err := settle(fmt.Sprintf("Can't ask %s (%s) about %s: %v", ns.Name, ns.Addr.Addr(), name, err))
				return z.NegativeTTL, err
			}
			if !contains(addrs, want) {
				ok = false
				if said[ns.Name] == "" {
					said[ns.Name] = fmt.Sprintf("%s (%s) answered %s", ns.Name, ns.Addr.Addr(), describe(addrs))
				}
			}
		}
		if ok {
			log.Info("%s's nameservers (%s) answer %s", zone, strings.Join(names, ", "), want)
			return z.NegativeTTL, nil
		}
		var parts []string
		for _, n := range names {
			if said[n] != "" && last[n] != said[n] {
				log.Info("%s", said[n])
			}
			last[n] = said[n]
			if said[n] != "" {
				parts = append(parts, said[n])
			}
		}
		now := w.Clock.Now()
		if !now.Before(deadline) {
			return 0, fmt.Errorf("%w: %s's own nameservers don't serve it: %s", ErrNotVisible, zone, strings.Join(parts, "; "))
		}
		if err := w.Clock.Sleep(ctx, min(w.AuthEvery, deadline.Sub(now))); err != nil {
			return 0, err
		}
	}
}

// human is a duration as the job log shows it: "30 min", "10 s".
func human(d time.Duration) string {
	switch {
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d min", d/time.Minute)
	case d >= time.Second && d%time.Second == 0:
		return fmt.Sprintf("%d s", d/time.Second)
	case d >= time.Minute:
		return d.Round(time.Second).String()
	}
	return d.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func contains(addrs []netip.Addr, a netip.Addr) bool {
	for _, x := range addrs {
		if x == a {
			return true
		}
	}
	return false
}

func describe(addrs []netip.Addr) string {
	if len(addrs) == 0 {
		return "nothing"
	}
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	sort.Strings(s)
	return strings.Join(s, ", ")
}

// lookup asks over DoH, and over plain UDP when the DoH request itself fails.
func (w Waiter) lookup(ctx context.Context, r Resolver, name string) ([]netip.Addr, string, error) {
	addrs, err := w.doh(ctx, r, name)
	if err == nil {
		return addrs, "doh", nil
	}
	if ctx.Err() != nil {
		return nil, "doh", ctx.Err()
	}
	if r.UDP == "" {
		return nil, "doh", err
	}
	addrs, uerr := udp(ctx, r, name)
	if uerr != nil {
		return nil, "udp", fmt.Errorf("DoH: %v; UDP: %v", err, uerr)
	}
	return addrs, "udp", nil
}

// doh is RFC 8484: a GET with the query in the dns parameter.
func (w Waiter) doh(ctx context.Context, r Resolver, name string) ([]netip.Addr, error) {
	ans, err := w.dohQuery(ctx, r, name, dnsmessage.TypeA)
	if err != nil {
		return nil, err
	}
	return aRecords(ans), nil
}

// dohQuery asks r one question over DoH. An NXDOMAIN is an answer.
func (w Waiter) dohQuery(ctx context.Context, r Resolver, name string, t dnsmessage.Type) (dnsmessage.Message, error) {
	var ans dnsmessage.Message
	msg, err := question(name, t, true)
	if err != nil {
		return ans, err
	}
	wire, err := msg.Pack()
	if err != nil {
		return ans, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.DoH+"?dns="+base64.RawURLEncoding.EncodeToString(wire), nil)
	if err != nil {
		return ans, err
	}
	req.Header.Set("Accept", "application/dns-message")
	client := w.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ans, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ans, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ans, err
	}
	if err := ans.Unpack(body); err != nil {
		return ans, fmt.Errorf("unreadable DNS answer: %w", err)
	}
	switch ans.RCode {
	case dnsmessage.RCodeSuccess, dnsmessage.RCodeNameError:
	default:
		return ans, fmt.Errorf("DNS %s", ans.RCode)
	}
	return ans, nil
}

func question(name string, t dnsmessage.Type, recursive bool) (dnsmessage.Message, error) {
	n, err := dnsmessage.NewName(strings.TrimSuffix(name, ".") + ".")
	if err != nil {
		return dnsmessage.Message{}, err
	}
	return dnsmessage.Message{
		Header:    dnsmessage.Header{RecursionDesired: recursive},
		Questions: []dnsmessage.Question{{Name: n, Type: t, Class: dnsmessage.ClassINET}},
	}, nil
}

func aRecords(m dnsmessage.Message) []netip.Addr {
	var out []netip.Addr
	for _, rr := range m.Answers {
		if a, ok := rr.Body.(*dnsmessage.AResource); ok {
			out = append(out, netip.AddrFrom4(a.A))
		}
	}
	return out
}

// udp asks the resolver's port 53 directly.
func udp(ctx context.Context, r Resolver, name string) ([]netip.Addr, error) {
	res := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", r.UDP)
	}}
	addrs, err := res.LookupNetIP(ctx, "ip4", name)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return nil, nil // an answer: the name does not exist (yet)
	}
	return addrs, err
}
