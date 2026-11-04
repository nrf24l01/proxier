package pages_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/tikhonp/proxier/internal/modules/routing/routingtest"
	"github.com/tikhonp/proxier/internal/modules/routing/shadowrocket"
	sragent "github.com/tikhonp/proxier/internal/modules/routing/shadowrocket/agent"
	"github.com/tikhonp/proxier/internal/platform/access"
	"github.com/tikhonp/proxier/internal/platform/events"
)

var formIDR = regexp.MustCompile(`name="form_id" value="([0-9a-f]+)"`)

func TestShadowrocketHandOff(t *testing.T) {
	h, id, _ := srSetup(t)
	const cfg = "/routing/shadowrocket/1"
	if _, _, err := h.Mod.Shadowrocket.SaveBase(bg(), id, access.All, 0, srBase+"[Proxy]\nHome = vless, 203.0.113.9, 443, password=pw-1\n", "", "admin"); err != nil {
		t.Fatal(err)
	}

	page := h.Login.Get(cfg).Body.String()
	has(t, "config page", page, `href="`+cfg+`/agent"`, "Hand off to an agent…")
	if strings.Contains(page, "Handed to") {
		t.Error("a band with no session")
	}

	form := h.Login.Get(cfg + "/agent").Body.String()
	has(t, "form", form, "Hand off the base config to an agent", "iphone.conf · base config v2", `name="problem"`, `value="claude-code" checked`, `value="120" checked`,
		"The agent reads these values as MASKED-BY-PROXIER and never sees them: [Proxy] Home.",
		"Every save the agent makes is a new base version, and the phone gets it on its next refresh.",
		"change the routing list, the policy or the URL")
	checkNoInline(t, "form", form)
	fid := formIDR.FindStringSubmatch(form)
	if fid == nil {
		t.Fatal("no form id")
	}

	// refused: nothing opened
	if rec := h.Login.Post(cfg+"/agent", url.Values{"problem": {" "}, "agent": {"codex"}, "for": {"30"}}); rec.Code != 422 || !strings.Contains(rec.Body.String(), "Say what the agent should do") {
		t.Errorf("empty problem: %d", rec.Code)
	}
	if n := len(h.Events("routing.shadowrocket_agent_session_opened")); n != 0 {
		t.Fatalf("a refused form opened %d sessions", n)
	}

	post := url.Values{"form_id": {fid[1]}, "problem": {"Shadowrocket shows an error at line 3\nsecond line"}, "agent": {"codex"}, "for": {"30"}}
	rec := h.Login.Post(cfg+"/agent", post)
	out := rec.Body.String()
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("open: %d %v", rec.Code, rec.Header())
	}
	has(t, "result", out, "You are Codex.", "pxa_••••••••••••", "Copy prompt", `data-copy="You are Codex.`, "revoke access on the config page")
	if !regexp.MustCompile(`data-copy="[^"]*pxa_[A-Za-z0-9_-]{20,}`).MatchString(out) {
		t.Error("the copy button has no real token")
	}
	if strings.Contains(out, "pw-1") {
		t.Error("the result page holds a hidden value")
	}
	// the reloaded answer opens nothing
	has(t, "reloaded", h.Login.Post(cfg+"/agent", post).Body.String(), "Already copied")
	if n := len(h.Events("routing.shadowrocket_agent_session_opened")); n != 1 {
		t.Fatalf("%d sessions opened", n)
	}

	sess, err := h.Mod.ShadowrocketAgent.Current(bg(), id)
	if err != nil {
		t.Fatal(err)
	}
	sid := i64s(sess.ID)
	page = h.Login.Get(cfg).Body.String()
	has(t, "band", page, "Handed to Codex", "Shadowrocket shows an error at line 3", "0 saves · expires ",
		"/activity?module=routing&amp;actor=agent:"+sid, `action="`+cfg+`/agent/`+sid+`/revoke"`, "Handed the base config of iphone to an agent")
	if strings.Contains(page, "second line") {
		t.Error("the band shows more than the problem's first line")
	}

	// the editor polls while the session is open, and says when the agent saved
	editor := h.Login.Get(cfg + "/base").Body.String()
	has(t, "editor", editor, "Handed to Codex", `id="ed-poll"`, `hx-get="`+cfg+`/base/version"`, `hx-include="#ed-revision"`, `id="ed-revision" name="version" value="2"`)
	poll := func(v string) string {
		return h.Login.Get(cfg+"/base/version?version="+v, http.Header{"Hx-Request": {"true"}}).Body.String()
	}
	if p := poll("2"); !strings.Contains(p, `id="ed-poll"`) || strings.Contains(p, "data-ed-stale") {
		t.Errorf("poll, unchanged: %s", p)
	}
	if _, _, err := h.Mod.Shadowrocket.SaveBase(bg(), id, access.All, 2, srBase+"# by the agent\n", "", events.AgentActor(sess.ID)); err != nil {
		t.Fatal(err)
	}
	if p := poll("2"); !strings.Contains(p, "data-ed-stale") || !strings.Contains(p, "an agent or another tab saved it") || strings.Contains(p, "hx-get") {
		t.Errorf("poll, changed: %s", p)
	}
	// a save from the editor that opened v2 is refused, and keeps the text
	rec = h.Login.Post(cfg+"/base", url.Values{"base": {srBase + "# by the member\n"}, "version": {"2"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale save: %d", rec.Code)
	}
	has(t, "stale save", rec.Body.String(), "The base config changed since you opened it", "# by the member", `data-code-save disabled`, `href="`+cfg+`/base"`)
	if v, _ := h.Mod.Shadowrocket.Version(bg(), id, 0); v.Number != 3 || !strings.Contains(v.Content, "# by the agent") {
		t.Errorf("after the stale save: v%d", v.Number)
	}
	if rec := h.Login.Post(cfg+"/base", url.Values{"base": {srBase + "# by the member\n"}, "version": {"3"}}); rec.Code != 303 {
		t.Errorf("save on v3: %d", rec.Code)
	}

	// revoke: through another config's page it's not found
	other, err := h.Mod.Shadowrocket.Create(bg(), routingtest.Owner, shadowrocket.New{Name: "ipad", ListID: 1, Policy: "PROXY", Base: srBase}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if rec := h.Login.Post("/routing/shadowrocket/"+i64s(other)+"/agent/"+sid+"/revoke", nil); rec.Code != 404 {
		t.Errorf("revoke through another config: %d", rec.Code)
	}
	if rec := h.Login.Post(cfg+"/agent/"+sid+"/revoke", nil); rec.Code != 303 || rec.Header().Get("Location") != cfg+"?revoked=1" {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	page = h.Login.Get(cfg + "?revoked=1").Body.String()
	has(t, "revoked", page, "The agent&#39;s access was revoked.")
	if strings.Contains(page, "Handed to") {
		t.Error("the band stays after revoking")
	}
	if p := poll("4"); strings.Contains(p, "ed-poll") {
		t.Errorf("the poll goes on after the session: %s", p)
	}
	if strings.Contains(h.Login.Get(cfg+"/base").Body.String(), `id="ed-poll"`) {
		t.Error("the editor polls without a session")
	}
}

func TestShadowrocketHandOffNeedsTheConfig(t *testing.T) {
	h, _, _ := srSetup(t)
	anna := h.Site.Join("anna", ownGrant(), "")
	vera := h.Site.Join("vera", access.PresetViewer.Grant(), "")
	// another member's config: not found at own, forbidden to a reader of all
	if rec := anna.Get("/routing/shadowrocket/1/agent"); rec.Code != 404 {
		t.Errorf("anna: %d", rec.Code)
	}
	if rec := vera.Get("/routing/shadowrocket/1/agent"); rec.Code != 403 {
		t.Errorf("vera: %d", rec.Code)
	}
	if rec := vera.Post("/routing/shadowrocket/1/agent", url.Values{"problem": {"x"}, "agent": {"codex"}}); rec.Code != 403 {
		t.Errorf("vera's post: %d", rec.Code)
	}
	if strings.Contains(vera.Get("/routing/shadowrocket/1").Body.String(), "Hand off to an agent") {
		t.Error("a reader sees Hand off")
	}
	// a reader sees an open session's band, without Revoke
	if _, err := h.Mod.ShadowrocketAgent.OpenSession(bg(), sragent.Open{ConfigID: 1, Problem: "fix it", Agent: "other"}, "admin"); err != nil {
		t.Fatal(err)
	}
	page := vera.Get("/routing/shadowrocket/1").Body.String()
	has(t, "reader's band", page, "Handed to Other")
	if strings.Contains(page, "/revoke") {
		t.Error("a reader can revoke")
	}
	// at own, their own config can be handed off
	mem, err := h.App.Auth.MemberByUsername(bg(), "anna")
	if err != nil {
		t.Fatal(err)
	}
	annaW := access.Who{Member: mem.ID, Grant: ownGrant()}
	cid, err := h.Mod.Shadowrocket.Create(bg(), annaW, shadowrocket.New{Name: "anna-phone", ListID: h.ListOf(annaW, "Anna's"), Policy: "PROXY", Base: srBase}, events.MemberActor(mem.ID))
	if err != nil {
		t.Fatal(err)
	}
	if rec := anna.Get("/routing/shadowrocket/" + i64s(cid) + "/agent"); rec.Code != 200 {
		t.Errorf("anna's own: %d", rec.Code)
	}
}
