package agent

import (
	"strings"
	"testing"
)

const secretBase = "[General]\nbypass-system = true\n\n[Proxy]\n# my home box\nHome = vless, 203.0.113.9, 443, password=pw-home-1\nWork = trojan,198.51.100.2,443,password=pw-work-2\n\n" +
	"[Proxy Group]\nPROXY = select,Home,Work\n\n[Rule]\nDOMAIN-SUFFIX,example.com,PROXY\nFINAL,DIRECT\n\n" +
	"[MITM]\nhostname = *.example.com\nca-passphrase = pass-3\nca-p12 = MIIKpkey4\n"

func TestMaskHidesProxyLinesAndTheMITMKey(t *testing.T) {
	got := maskBody(secretBase)
	for _, leak := range []string{"pw-home-1", "pw-work-2", "203.0.113.9", "pass-3", "MIIKpkey4"} {
		if strings.Contains(got, leak) {
			t.Errorf("masked base holds %q", leak)
		}
	}
	for _, want := range []string{"Home = " + Masked + "\n", "Work = " + Masked + "\n", "ca-passphrase = " + Masked + "\n", "ca-p12 = " + Masked + "\n",
		"PROXY = select,Home,Work\n", "hostname = *.example.com\n", "bypass-system = true\n", "# my home box\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("masked base has no %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") != strings.Count(secretBase, "\n") {
		t.Error("masking changed the number of lines")
	}
	spots := Spots(secretBase)
	if len(spots) != 4 || spots[0] != (Spot{Section: "Proxy", Key: "Home", Line: 6}) || spots[3] != (Spot{Section: "MITM", Key: "ca-p12", Line: 19}) {
		t.Errorf("spots: %+v", spots)
	}
	if b := "[General]\n[Rule]\nFINAL,DIRECT\n"; maskBody(b) != b || len(Spots(b)) != 0 {
		t.Error("a base with nothing hidden changed")
	}
}

func TestUnmaskPutsTheStoredValuesBack(t *testing.T) {
	edited := strings.Replace(maskBody(secretBase), "DOMAIN-SUFFIX,example.com,PROXY\n", "DOMAIN-SUFFIX,example.com,PROXY\nDOMAIN-SUFFIX,example.org,PROXY\n", 1)
	got, err := unmask(edited, secretBase)
	want := strings.Replace(secretBase, "DOMAIN-SUFFIX,example.com,PROXY\n", "DOMAIN-SUFFIX,example.com,PROXY\nDOMAIN-SUFFIX,example.org,PROXY\n", 1)
	if err != nil || got != want {
		t.Fatalf("unmask: %v\n%s", err, got)
	}
	// moved lines, spacing the agent changed and CRLF keep the real values
	crlf := strings.ReplaceAll(secretBase, "\n", "\r\n")
	moved := strings.ReplaceAll(strings.Replace(maskBody(crlf), "Work = "+Masked, "Work   =   "+Masked, 1), "[MITM]\r\nhostname", "[mitm]\r\nhostname")
	got, err = unmask(moved, crlf)
	if err != nil || !strings.Contains(got, "Work   = trojan,198.51.100.2,443,password=pw-work-2\r\n") || !strings.Contains(got, "ca-p12 = MIIKpkey4\r\n") {
		t.Errorf("moved: %v\n%q", err, got)
	}
	// a real value written over the mask is the new value
	got, _ = unmask(strings.Replace(maskBody(secretBase), "Home = "+Masked, "Home = vless, 192.0.2.1, 443, password=new", 1), secretBase)
	if !strings.Contains(got, "password=new") || strings.Contains(got, "pw-home-1") {
		t.Errorf("replaced value: %s", got)
	}
	// a base with no mask is taken as it is
	if got, err := unmask("[Rule]\nFINAL,DIRECT\n", secretBase); err != nil || got != "[Rule]\nFINAL,DIRECT\n" {
		t.Errorf("no mask: %q %v", got, err)
	}
}

func TestUnmaskRefusesAMaskWithNoValue(t *testing.T) {
	m := maskBody(secretBase)
	for name, body := range map[string]string{
		"renamed proxy": strings.Replace(m, "Home = ", "House = ", 1),
		// Home twice, Work gone: the second Home has no stored value left
		"twice":             strings.Replace(m, "Work = "+Masked, "Home = "+Masked, 1),
		"another section":   strings.Replace(m, "bypass-system = true", "bypass-system = "+Masked, 1),
		"a rule":            strings.Replace(m, "FINAL,DIRECT", "FINAL,"+Masked, 1),
		"inside a value":    strings.Replace(m, "hostname = *.example.com", "hostname = *."+Masked, 1),
		"mitm key renamed":  strings.Replace(m, "ca-passphrase", "passphrase", 1),
		"proxy into a list": strings.Replace(m, "[Proxy Group]\nPROXY = select,Home,Work", "[Proxy Group]\nPROXY = "+Masked, 1),
	} {
		if _, err := unmask(body, secretBase); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
