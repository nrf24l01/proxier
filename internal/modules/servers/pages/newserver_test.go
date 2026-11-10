package pages_test

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tikhonp/proxier/internal/modules/servers"
	"github.com/tikhonp/proxier/internal/modules/servers/serverstest"
	"github.com/tikhonp/proxier/internal/modules/servers/store"
)

// routedGuard covers every hostname under proxier.test, as a routing list
// holding that suffix would.
type routedGuard struct{ asked []string }

func (g *routedGuard) Covering(_ context.Context, hostnames []string) ([]servers.RoutedName, error) {
	g.asked = append(g.asked, hostnames...)
	var out []servers.RoutedName
	for _, h := range hostnames {
		if strings.HasSuffix(h, ".proxier.test") {
			out = append(out, servers.RoutedName{Hostname: h, Domain: "proxier.test", Service: "my-sites", List: "Main"})
		}
	}
	return out, nil
}

func TestNewServerRefusedWhenRouted(t *testing.T) {
	h := serverstest.NewHarness(t, serverstest.StubProxy())
	g := &routedGuard{}
	h.Mod.SetRouting(g)
	f := h.Form()
	form := url.Values{
		"ip": {f.IP}, "ssh_port": {strconv.Itoa(f.SSHPort)}, "root_password": {f.RootPassword}, "country": {h.Country},
		"template": {sid(h.TemplateID)}, "version": {"1"}, "param.letsencrypt_email": {"me@example.com"},
	}
	const want = "nl-1.hosts.proxier.test is covered by proxier.test (my-sites) in Main: Proxier&#39;s checks would go into the tunnel. Take it out of the list first."

	// the live summary says it while the admin types
	rec := h.Login.Post("/servers/new/summary", url.Values{
		"ip": {"203.0.113.24"}, "country": {h.Country},
		"template": {sid(h.TemplateID)}, "version": {"1"}, "params_for": {sid(h.TemplateID) + ":1"},
	})
	mustContain(t, rec.Body.String(), want)

	rec = h.Login.Post("/servers", form)
	if rec.Code != 422 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body)
	}
	mustContain(t, rec.Body.String(), want)
	if srvs, _ := store.ListServers(bg, h.App.DB.R); len(srvs) != 0 {
		t.Fatalf("created anyway: %+v", srvs)
	}
	if len(g.asked) == 0 || g.asked[len(g.asked)-1] != "nl-1.hosts.proxier.test" {
		t.Errorf("asked %v", g.asked)
	}

	// without a guard nothing is checked
	h.Mod.SetRouting(nil)
	rec = h.Login.Post("/servers", form)
	if rec.Code != 303 {
		t.Fatalf("without a guard: %d\n%s", rec.Code, rec.Body)
	}
}

func TestNewServerTemplateVersionRefresh(t *testing.T) {
	h := serverstest.NewHarness(t, serverstest.StubProxy())
	for i := 2; i <= 6; i++ {
		h.PublishVersion(func(files map[string][]byte) {
			files["site/index.html"] = append(files["site/index.html"], []byte("<!-- revision -->")...)
		}, true)
	}
	form := url.Values{
		"template": {sid(h.TemplateID)}, "version": {"1"}, "params_for": {"0:1"}, "country": {"NL"},
	}
	// Switching templates replaces the stale dropdown with every version and
	// selects the new template's default, even when both templates have v1.
	rec := h.Login.Post("/servers/new/summary", form)
	if rec.Code != 200 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	mustContain(t, body, `id="srv-ver"`, `hx-swap-oob="true"`, `name="params_for" value="`+sid(h.TemplateID)+`:6"`)
	for i := 1; i <= 6; i++ {
		mustContain(t, body, `<option value="`+strconv.Itoa(i)+`"`)
	}
	mustContain(t, body, `<option value="6" selected`)

	// Explicitly selecting an older version of the same template is kept.
	form.Set("params_for", sid(h.TemplateID)+":6")
	rec = h.Login.Post("/servers/new/summary", form)
	mustContain(t, rec.Body.String(), `<option value="1" selected`, `name="params_for" value="`+sid(h.TemplateID)+`:1"`)

	// Ordinary edits leave the dropdown and parameter inputs untouched.
	form.Set("params_for", sid(h.TemplateID)+":1")
	rec = h.Login.Post("/servers/new/summary", form)
	mustNotContain(t, rec.Body.String(), `id="srv-ver"`, `id="params"`)

	// Apply provisions the version selected in the refreshed dropdown.
	f := h.Form()
	form.Set("ip", f.IP)
	form.Set("root_password", f.RootPassword)
	form.Set("version", "6")
	form.Set("param.letsencrypt_email", "me@example.com")
	rec = h.Login.Post("/servers", form)
	if rec.Code != 303 {
		t.Fatalf("create: %d\n%s", rec.Code, rec.Body)
	}
	srvs, err := store.ListServers(bg, h.App.DB.R)
	if err != nil || len(srvs) != 1 {
		t.Fatalf("servers: %v, %v", srvs, err)
	}
	if srvs[0].TemplateVersion != 6 {
		t.Fatalf("provisioned version %d, want 6", srvs[0].TemplateVersion)
	}
}

// The hostname typed in the form is the one checked, shown and kept.
func TestNewServerCustomHostname(t *testing.T) {
	h := serverstest.NewHarness(t, serverstest.StubProxy())
	g := &routedGuard{}
	h.Mod.SetRouting(g)
	f := h.Form()
	form := url.Values{
		"ip": {f.IP}, "ssh_port": {strconv.Itoa(f.SSHPort)}, "root_password": {f.RootPassword}, "country": {h.Country},
		"template": {sid(h.TemplateID)}, "version": {"1"}, "hostname": {"vpn.proxier.test"},
	}
	const want = "vpn.proxier.test is covered by proxier.test (my-sites) in Main"
	rec := h.Login.Post("/servers/new/summary", url.Values{
		"ip": {"203.0.113.24"}, "country": {h.Country}, "hostname": {"vpn.proxier.test"},
		"template": {sid(h.TemplateID)}, "version": {"1"}, "params_for": {sid(h.TemplateID) + ":1"},
	})
	mustContain(t, rec.Body.String(), want)

	rec = h.Login.Post("/servers", form)
	if rec.Code != 422 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body)
	}
	mustContain(t, rec.Body.String(), want)
	mustContain(t, rec.Body.String(), `name="hostname" value="vpn.proxier.test"`)

	form.Set("hostname", "vpn")
	h.Mod.SetRouting(nil)
	rec = h.Login.Post("/servers", form)
	if rec.Code != 422 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body)
	}
	mustContain(t, rec.Body.String(), "Enter a full DNS name such as vpn.example.com.")

	form.Set("hostname", "vpn.proxier.test")
	if rec = h.Login.Post("/servers", form); rec.Code != 303 {
		t.Fatalf("%d\n%s", rec.Code, rec.Body)
	}
	h.Drain()
	if srvs, _ := store.ListServers(bg, h.App.DB.R); len(srvs) != 1 || srvs[0].ManagementHostname != "vpn.proxier.test" {
		t.Fatalf("servers: %+v", srvs)
	}
}
