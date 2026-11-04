package pages_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/tikhonp/proxier/internal/modules/servers/store"
)

func TestChangeIPPages(t *testing.T) {
	h, id := provisioned(t)
	base := "/servers/" + sid(id)

	// The Overview offers it; the dialog says what to set up first.
	mustContain(t, page(t, h, base), `href="`+base+`/ip"`)
	body := page(t, h, base+"/ip")
	mustContain(t, body, "Change the IP of nl-1", "Add the IP to the VPS at the hosting provider", "nl-1.hosts.tikhonnnnn.com → 127.0.0.1",
		`name="keep" value="1" checked`)
	mustNotContain(t, body, "This template&#39;s files use the IP") // the seed template names the hostname only

	// A refusal re-renders the form with the error and what was typed.
	rec := h.Login.Post(base+"/ip", url.Values{"ip": {"127.0.0.1"}, "keep": {"1"}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "nl-1 has this IP already.") {
		t.Fatalf("same IP: %d\n%s", rec.Code, excerpt(rec.Body.String()))
	}

	rec = h.Login.Post(base+"/ip", url.Values{"ip": {"10.77.0.5"}, "keep": {"1"}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), base+"?job=") {
		t.Fatalf("change: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	h.Drain()
	if s := h.Server(id); s.IP != "10.77.0.5" {
		t.Fatalf("ip %s", s.IP)
	}

	// The previous IP shows on the Overview and on the Health tab, with its actions.
	mustContain(t, page(t, h, base), ">Previous IPs<", ">127.0.0.1<")
	health := page(t, h, base+"/health")
	mustContain(t, health, ">Previous IPs<", ">Switch back<", ">Forget<", `href="`+base+`/ip?ip=127.0.0.1"`)
	mustContain(t, page(t, h, base+"/ip?ip=127.0.0.1"), `value="127.0.0.1"`)

	prev, err := store.PreviousIPs(bg, h.App.DB.R, id)
	if err != nil || len(prev) != 1 {
		t.Fatalf("previous: %+v %v", prev, err)
	}
	rec = h.Login.Post(base+"/previous/"+sid(prev[0].ID)+"/forget", url.Values{})
	if rec.Code != 303 {
		t.Fatalf("forget: %d", rec.Code)
	}
	mustNotContain(t, page(t, h, base+"/health"), ">Previous IPs<")
	if rec := h.Login.Post(base+"/previous/"+sid(prev[0].ID)+"/forget", url.Values{}); rec.Code != 404 {
		t.Fatalf("forget twice: %d", rec.Code)
	}
}
