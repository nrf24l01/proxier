package dns

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Found is what one check of a record made by hand found.
type Found struct {
	// OK: every answer is the IP, and nothing else.
	OK bool
	// Zone is the zone the name was found in, "" when its nameservers could
	// not be found.
	Zone string
	// Said is who answered what: "ns1.example.net (192.0.2.53) answered nothing".
	Said string
}

// Apex finds the zone name is in, for a name in none of the Cloudflare zones:
// the nearest of its parents (or the name itself, when it has two labels)
// that has nameservers. It asks only about the parents, never about the name,
// so no resolver caches a "no such name" for it. "" when none has any.
func (w Waiter) Apex(ctx context.Context, name string) string {
	zone, _, _ := w.defaults().zoneOf(ctx, name)
	return zone
}

func (w Waiter) zoneOf(ctx context.Context, name string) (string, Zone, error) {
	labels := strings.Split(strings.TrimSuffix(strings.ToLower(name), "."), ".")
	start := 1
	if len(labels) == 2 {
		start = 0
	}
	var errs []string
	for i := start; i <= len(labels)-2; i++ {
		cand := strings.Join(labels[i:], ".")
		z, err := w.FindZone(ctx, cand)
		if ctx.Err() != nil {
			return "", z, ctx.Err()
		}
		if err == nil && len(z.Servers) > 0 {
			return cand, z, nil
		}
		if err != nil {
			errs = append(errs, cand+": "+err.Error())
		}
	}
	if len(errs) == 0 {
		return "", Zone{}, fmt.Errorf("%s has no parent zone", name)
	}
	return "", Zone{}, fmt.Errorf("no nameservers found for %s (%s)", name, strings.Join(errs, "; "))
}

// Check asks once whether name points to ip and nowhere else. It asks the
// zone's own nameservers, which cache nothing, so a check made before the
// record exists does not delay it later. When they can't be asked it asks
// the public resolvers instead. err is set only when no one could be asked.
func (w Waiter) Check(ctx context.Context, name, ip string) (Found, error) {
	want, err := netip.ParseAddr(ip)
	if err != nil {
		return Found{}, fmt.Errorf("dns: %q is not an IP address", ip)
	}
	w = w.defaults()
	zone, z, zerr := w.zoneOf(ctx, name)
	if ctx.Err() != nil {
		return Found{}, ctx.Err()
	}
	if zerr == nil {
		f, err := w.checkAuthoritative(ctx, z, name, want)
		if err == nil {
			f.Zone = zone
			return f, nil
		}
		if ctx.Err() != nil {
			return Found{}, ctx.Err()
		}
		zerr = err
	}
	f, err := w.checkPublic(ctx, name, want)
	if err != nil {
		return Found{}, fmt.Errorf("%v; %w", zerr, err)
	}
	f.Zone = zone
	return f, nil
}

func (w Waiter) checkAuthoritative(ctx context.Context, z Zone, name string, want netip.Addr) (Found, error) {
	f := Found{OK: true}
	var said []string
	seen := map[string]bool{}
	for _, ns := range z.Servers {
		addrs, err := w.Ask(ctx, ns, name)
		if err != nil {
			return Found{}, fmt.Errorf("can't ask %s (%s): %w", ns.Name, ns.Addr.Addr(), err)
		}
		if !only(addrs, want) {
			f.OK = false
		}
		line := fmt.Sprintf("%s answered %s", ns.Name, describe(addrs))
		if !seen[line] {
			seen[line] = true
			said = append(said, line)
		}
	}
	f.Said = strings.Join(said, "; ")
	return f, nil
}

func (w Waiter) checkPublic(ctx context.Context, name string, want netip.Addr) (Found, error) {
	f := Found{OK: true}
	var said, errs []string
	for _, r := range w.Resolvers {
		addrs, _, err := w.Lookup(ctx, r, name)
		if err != nil {
			errs = append(errs, r.Name+": "+err.Error())
			continue
		}
		if !only(addrs, want) {
			f.OK = false
		}
		said = append(said, fmt.Sprintf("%s answered %s", r.Name, describe(addrs)))
	}
	if len(said) == 0 {
		return Found{}, errors.New(strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		f.OK = false
		said = append(said, errs...)
	}
	f.Said = strings.Join(said, "; ")
	return f, nil
}

// only: addrs is want alone. A second address would send some clients
// elsewhere.
func only(addrs []netip.Addr, want netip.Addr) bool {
	if len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if a != want {
			return false
		}
	}
	return true
}
