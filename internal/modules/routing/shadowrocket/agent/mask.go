package agent

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Masked stands in for every hidden value the agent reads.
const Masked = "MASKED-BY-PROXIER"

// Hidden values are the ones that unlock something on their own: a [Proxy]
// line holds a server's address and password, and [MITM]'s ca-p12 and
// ca-passphrase are the phone's MITM certificate authority with its private
// key. Proxies normally come from the link subscription, so most bases have
// none of them.
var mitmKeys = map[string]bool{"ca-p12": true, "ca-passphrase": true}

var sectionLine = regexp.MustCompile(`^\[([^\]]+)\]$`)

// Spot is one hidden value: where it is and under which key.
type Spot struct {
	Section string `json:"section"`
	Key     string `json:"key"`
	Line    int    `json:"line"`
}

// line is one line of a base cut where a value can be hidden.
type line struct {
	text    string // without its terminator
	end     string // "\n", "\r\n" or ""
	section string // the section it is in, lower case
	key     string // "" when the line holds no value that can be hidden
	eq      int    // index of the '=' in text
}

func (l line) value() string { return l.text[l.eq+1:] }

func split(body string) []line {
	var out []line
	section := ""
	for body != "" {
		var l line
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			l.text, body = body[:i], body[i+1:]
			l.end = "\n"
			if strings.HasSuffix(l.text, "\r") {
				l.text, l.end = l.text[:len(l.text)-1], "\r\n"
			}
		} else {
			l.text, body = body, ""
		}
		t := strings.TrimSpace(l.text)
		if m := sectionLine.FindStringSubmatch(t); m != nil {
			section = strings.ToLower(strings.TrimSpace(m[1]))
		}
		l.section = section
		if eq := strings.IndexByte(l.text, '='); eq > 0 && !comment(t) && strings.TrimSpace(l.text[eq+1:]) != "" {
			key := strings.TrimSpace(l.text[:eq])
			if section == "proxy" || section == "mitm" && mitmKeys[strings.ToLower(key)] {
				l.key, l.eq = key, eq
			}
		}
		out = append(out, l)
	}
	return out
}

func comment(t string) bool {
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "//")
}

// spotKey names a hidden value for matching a save to the stored base:
// proxy names keep their case, MITM keys don't have one.
func spotKey(section, key string) string {
	if section == "mitm" {
		key = strings.ToLower(key)
	}
	return section + "\x00" + key
}

// sectionName is how the agent reads a section's name.
func sectionName(section string) string {
	if section == "mitm" {
		return "MITM"
	}
	return "Proxy"
}

// Spots lists the base's hidden values.
func Spots(body string) []Spot {
	var out []Spot
	for i, l := range split(body) {
		if l.key != "" {
			out = append(out, Spot{Section: sectionName(l.section), Key: l.key, Line: i + 1})
		}
	}
	return out
}

// maskBody is the base as the agent reads it: every hidden value replaced by
// Masked, the space around it and every other byte kept.
func maskBody(body string) string {
	var b strings.Builder
	b.Grow(len(body))
	for _, l := range split(body) {
		if l.key == "" {
			b.WriteString(l.text + l.end)
			continue
		}
		v := l.value()
		lead := v[:len(v)-len(strings.TrimLeft(v, " \t"))]
		b.WriteString(l.text[:l.eq+1] + lead + Masked + l.end)
	}
	return b.String()
}

var errMaskOutside = errors.New(Masked + " is outside a hidden value")

// unmask puts the stored base's hidden values back into a base the agent
// wrote: where a hidden key still reads Masked, its stored value returns, in
// order when a key is there more than once. A real value written there
// replaces it. A mask on a key that held no hidden value, or anywhere else,
// is an error, so the mask never becomes a value.
func unmask(body, stored string) (string, error) {
	if !strings.Contains(body, Masked) {
		return body, nil
	}
	kept := map[string][]string{}
	for _, l := range split(stored) {
		if l.key != "" {
			k := spotKey(l.section, l.key)
			kept[k] = append(kept[k], l.value())
		}
	}
	var b strings.Builder
	b.Grow(len(body))
	for _, l := range split(body) {
		if l.key == "" || strings.TrimSpace(l.value()) != Masked {
			if strings.Contains(l.text, Masked) {
				return "", errMaskOutside
			}
			b.WriteString(l.text + l.end)
			continue
		}
		k := spotKey(l.section, l.key)
		vals := kept[k]
		if len(vals) == 0 {
			return "", fmt.Errorf("%s on %s in [%s] holds no hidden value", Masked, l.key, sectionName(l.section))
		}
		kept[k] = vals[1:]
		b.WriteString(l.text[:l.eq+1] + vals[0] + l.end)
	}
	return b.String(), nil
}
