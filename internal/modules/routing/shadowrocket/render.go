// Package shadowrocket hosts a Shadowrocket config per phone: the admin's
// base config, versioned, with the routing list's rules spliced into its
// [Rule] section on every fetch of GET /r/{token}/{name}.conf
// (docs/processes/routing/shadowrocket-config.md,
// docs/integrations/shadowrocket.md). Rendering is pure (render.go,
// rules.go); configs, versions and the fetch log are the Service.
package shadowrocket

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// What a base config or a lookup is refused for.
var (
	ErrNoRule   = errors.New("shadowrocket: no [Rule] section to add the services to")
	ErrTooBig   = errors.New("shadowrocket: the base config is over 256 KiB")
	ErrNotUTF8  = errors.New("shadowrocket: the base config is not UTF-8 text")
	ErrNotFound = errors.New("shadowrocket: no such config")
)

// MaxBase is the largest base config.
const MaxBase = 256 << 10

// Block is one service's rules, under its tag.
type Block struct {
	Tag   string
	Rules []Rule
}

// Rule is one name of a block: DOMAIN-SUFFIX, or DOMAIN when Exact.
type Rule struct {
	Name  string
	Exact bool
}

var header = regexp.MustCompile(`^\[[^\]]+\]$`)

// Validate checks what a base config must be: at most MaxBase bytes, UTF-8,
// with a [Rule] section. Nothing else is interpreted.
func Validate(base []byte) error {
	if len(base) > MaxBase {
		return ErrTooBig
	}
	if !utf8.Valid(base) {
		return ErrNotUTF8
	}
	if ruleStart(splitLines(base)) < 0 {
		return ErrNoRule
	}
	return nil
}

// splitLines splits keeping each line's terminator, so joining the lines
// gives the input back byte for byte.
func splitLines(b []byte) []string {
	var out []string
	s := string(b)
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

func trimmed(line string) string { return strings.TrimSpace(line) }

func ruleStart(lines []string) int {
	for i, l := range lines {
		if strings.EqualFold(trimmed(l), "[rule]") {
			return i
		}
	}
	return -1
}

// Render splices the blocks into base's [Rule] section: before its first
// FINAL line, or at the end of the section followed by FINAL,DIRECT, above
// the section's trailing blank lines either way. Everything else is copied
// byte for byte; the inserted lines take the base's line ending.
func Render(base []byte, list string, blocks []Block, policy string, at time.Time) ([]byte, error) {
	lines := splitLines(base)
	start, end, final, at0 := places(lines)
	if start < 0 {
		return nil, ErrNoRule
	}
	nl := "\n"
	if strings.HasSuffix(lines[0], "\r\n") {
		nl = "\r\n"
	}

	var b strings.Builder
	b.Grow(len(base) + 64*len(blocks))
	for _, l := range lines[:at0] {
		b.WriteString(l)
	}
	if at0 == len(lines) && at0 > 0 && !strings.HasSuffix(lines[at0-1], "\n") {
		b.WriteString(nl)
	}
	b.WriteString(nl)
	b.WriteString(`# Services from routing list "` + list + `", by Proxier (` + at.UTC().Format("2006-01-02 15:04") + " UTC)" + nl)
	for _, bl := range blocks {
		b.WriteString(nl + "# " + bl.Tag + nl)
		for _, r := range bl.Rules {
			kind := "DOMAIN-SUFFIX,"
			if r.Exact {
				kind = "DOMAIN,"
			}
			b.WriteString(kind + r.Name + "," + policy + nl)
		}
	}
	if final < 0 {
		b.WriteString("FINAL,DIRECT" + nl)
		if at0 == end && end < len(lines) {
			b.WriteString(nl)
		}
	}
	for _, l := range lines[at0:] {
		b.WriteString(l)
	}
	return []byte(b.String()), nil
}

// places finds, as line indexes, the [Rule] header (-1: none), the end of
// its section, its first FINAL line (-1: none) and the line the block goes
// before (len(lines): the end of the base).
func places(lines []string) (start, end, final, at int) {
	start = ruleStart(lines)
	if start < 0 {
		return -1, 0, -1, 0
	}
	end = len(lines)
	for i := start + 1; i < len(lines); i++ {
		if header.MatchString(trimmed(lines[i])) {
			end = i
			break
		}
	}
	final = -1
	for i := start + 1; i < end; i++ {
		if strings.HasPrefix(strings.ToUpper(trimmed(lines[i])), "FINAL,") {
			final = i
			break
		}
	}
	at = end
	if final >= 0 {
		at = final
	}
	for at > start+1 && trimmed(lines[at-1]) == "" {
		at--
	}
	return start, end, final, at
}

// Places are where Render puts the rules in a base, as line numbers from 1.
type Places struct {
	Rule   int // the [Rule] header; 0: none, and the base is refused
	Final  int // its first FINAL line; 0: none, and Proxier adds FINAL,DIRECT
	Before int // the line the rules go in before; 0: after the last line
}

// Where is where Render puts the rules in base.
func Where(base []byte) Places {
	lines := splitLines(base)
	start, _, final, at := places(lines)
	if start < 0 {
		return Places{}
	}
	p := Places{Rule: start + 1, Final: final + 1}
	if at < len(lines) {
		p.Before = at + 1
	}
	return p
}
