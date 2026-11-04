package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tikhonp/proxier/internal/modules/routing/routingtest"
	"github.com/tikhonp/proxier/internal/modules/routing/shadowrocket"
	"github.com/tikhonp/proxier/internal/modules/routing/shadowrocket/agent"
	"github.com/tikhonp/proxier/internal/platform/access"
	"github.com/tikhonp/proxier/internal/platform/events"
	"github.com/tikhonp/proxier/internal/platform/sitetest"
)

var bg = context.Background()

// base has a [Proxy] line and the MITM certificate, which the agent never sees.
const base = "[General]\nbypass-system = true\n\n[Proxy]\nHome = vless, 203.0.113.9, 443, password=pw-home-1\n\n" +
	"[Rule]\nRULE-SET,https://example.com/ads.list,REJECT\nFINAL,DIRECT\n\n" +
	"[MITM]\nhostname = *.example.com\nca-passphrase = pass-3\nca-p12 = MIIKpkey4\n"

var leaks = []string{"pw-home-1", "203.0.113.9", "pass-3", "MIIKpkey4"}

type env struct {
	T  *testing.T
	H  *routingtest.Harness
	ID int64
}

// newEnv: Main with anthropic, and a config iphone following it with base as v1.
func newEnv(t *testing.T) *env {
	t.Helper()
	h := routingtest.New(t)
	h.Up.V2fly("anthropic", "anthropic.com\nclaude.ai\n")
	h.List("Main", h.Upstream("anthropic"))
	id, err := h.Mod.Shadowrocket.Create(bg, routingtest.Owner, shadowrocket.New{Name: "iphone", ListID: 1, Policy: "PROXY", Base: base}, events.MemberActor(1))
	if err != nil {
		t.Fatal(err)
	}
	return &env{T: t, H: h, ID: id}
}

func (e *env) open(o agent.Open) agent.Opened {
	e.T.Helper()
	if o.ConfigID == 0 {
		o.ConfigID = e.ID
	}
	if o.Problem == "" {
		o.Problem = "Shadowrocket says the config has an error; fix it"
	}
	if o.Agent == "" {
		o.Agent = "claude-code"
	}
	op, err := e.H.Mod.ShadowrocketAgent.OpenSession(bg, o, events.MemberActor(1))
	if err != nil {
		e.T.Fatalf("open session: %v", err)
	}
	return op
}

type req struct {
	Method, Path string
	Token        string
	Header       map[string]string
	Body         string
}

func (e *env) do(r req) *httptest.ResponseRecorder {
	e.T.Helper()
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	h := http.Header{}
	if r.Token != "" {
		h.Set("Authorization", "Bearer "+r.Token)
	}
	for k, v := range r.Header {
		h.Set(k, v)
	}
	sr := sitetest.Req{Method: r.Method, Path: agent.APIPrefix + r.Path, Header: h}
	if r.Body != "" {
		sr.Body, sr.ContentType = []byte(r.Body), "text/plain"
	}
	return e.H.Site.Do(sr)
}

func (e *env) get(token, path string) *httptest.ResponseRecorder {
	e.T.Helper()
	return e.do(req{Path: path, Token: token})
}

func (e *env) put(token, etag, body string) *httptest.ResponseRecorder {
	e.T.Helper()
	hdr := map[string]string{}
	if etag != "" {
		hdr["If-Match"] = etag
	}
	return e.do(req{Method: http.MethodPut, Path: "/base", Token: token, Header: hdr, Body: body})
}

func (e *env) current() shadowrocket.Version {
	e.T.Helper()
	v, err := e.H.Mod.Shadowrocket.Version(bg, e.ID, 0)
	if err != nil {
		e.T.Fatal(err)
	}
	return v
}

func status(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
}

func noLeak(t *testing.T, what, s string) {
	t.Helper()
	for _, l := range leaks {
		if strings.Contains(s, l) {
			t.Errorf("%s holds the secret %q", what, l)
		}
	}
}

type checkJSON struct {
	OK          bool     `json:"ok"`
	Errors      []string `json:"errors"`
	RuleSection int      `json:"rule_section_line"`
	Final       int      `json:"final_line"`
	InsertAt    string   `json:"rules_inserted"`
	Hidden      []struct {
		Section, Key string
		Line         int
	} `json:"hidden"`
}

func TestOpenSessionRefusesAndRecords(t *testing.T) {
	e := newEnv(t)
	ag := e.H.Mod.ShadowrocketAgent
	for name, o := range map[string]agent.Open{
		"empty problem": {ConfigID: e.ID, Problem: "  ", Agent: "codex"},
		"long problem":  {ConfigID: e.ID, Problem: strings.Repeat("я", 4001), Agent: "codex"},
		"unknown agent": {ConfigID: e.ID, Problem: "x", Agent: "gpt"},
	} {
		if _, err := ag.OpenSession(bg, o, "admin"); !errors.Is(err, agent.ErrNoProblem) && !errors.Is(err, agent.ErrBadAgent) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := ag.OpenSession(bg, agent.Open{ConfigID: 99, Problem: "x", Agent: "codex"}, "admin"); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("no config: %v", err)
	}
	if n := len(e.H.Events("routing.shadowrocket_agent_session_opened")); n != 0 {
		t.Fatalf("refused hand-offs recorded %d events", n)
	}

	op := e.open(agent.Open{For: 24 * time.Hour})
	if want := e.H.Now.Add(8 * time.Hour); !op.ExpiresAt.Equal(want) {
		t.Errorf("expires %v, want the 8-hour cap %v", op.ExpiresAt, want)
	}
	for _, want := range []string{op.Token, "http://proxier.test" + agent.APIPrefix, "Shadowrocket says the config has an error", `"iphone.conf"`, "Every save is live", agent.Masked} {
		if !strings.Contains(op.Prompt, want) {
			t.Errorf("the prompt has no %q:\n%s", want, op.Prompt)
		}
	}
	noLeak(t, "the prompt", op.Prompt)
	ev := e.H.Events("routing.shadowrocket_agent_session_opened")
	if len(ev) != 1 || ev[0].Actor != events.MemberActor(1) || ev[0].Payload["agent"] != "claude-code" || ev[0].Subject != shadowrocket.Subject(e.ID) {
		t.Fatalf("opened: %+v", ev)
	}
	raw, _ := json.Marshal(ev[0].Payload)
	if strings.Contains(string(raw), "pxa_") {
		t.Error("the opened event holds the token")
	}
}

func TestEndedExpiredAndUnknownTokensLookTheSame(t *testing.T) {
	e := newEnv(t)
	first := e.open(agent.Open{For: 30 * time.Minute})
	status(t, e.get(first.Token, "/base"), http.StatusOK)
	second := e.open(agent.Open{})
	ended := e.get(first.Token, "/base")
	status(t, ended, http.StatusUnauthorized)
	closed := e.H.Events("routing.shadowrocket_agent_session_closed")
	if len(closed) != 1 || closed[0].Payload["reason"] != "replaced" {
		t.Fatalf("closed: %+v", closed)
	}
	unknown := e.get("pxa_"+strings.Repeat("A", 43), "/base")
	none := e.get("", "/base")
	cookie := e.H.Site.Do(sitetest.Req{Path: agent.APIPrefix + "/base", Cookies: []*http.Cookie{e.H.Login.Cookie}})
	for name, rec := range map[string]*httptest.ResponseRecorder{"unknown": unknown, "no token": none, "a session cookie": cookie} {
		if rec.Code != ended.Code || rec.Body.String() != ended.Body.String() || rec.Header().Get("WWW-Authenticate") != ended.Header().Get("WWW-Authenticate") {
			t.Errorf("%s: %d %s, an ended token: %d %s", name, rec.Code, rec.Body.String(), ended.Code, ended.Body.String())
		}
	}
	if !strings.Contains(ended.Body.String(), "session ended") {
		t.Errorf("ended: %s", ended.Body.String())
	}

	status(t, e.get(second.Token, "/base"), http.StatusOK)
	e.H.Advance(2*time.Hour + time.Second)
	status(t, e.get(second.Token, "/base"), http.StatusUnauthorized)
	// the schedule records the expiry; a hand-off after it says expired, not replaced
	n, err := e.H.Mod.ShadowrocketAgent.Expire(bg, "job:7")
	if err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	closed = e.H.Events("routing.shadowrocket_agent_session_closed")
	if len(closed) != 2 || closed[1].Payload["reason"] != "expired" || closed[1].Actor != "job:7" {
		t.Fatalf("closed: %+v", closed)
	}
	if n, _ := e.H.Mod.ShadowrocketAgent.Expire(bg, "job:8"); n != 0 {
		t.Errorf("a second scan closed %d", n)
	}
	third := e.open(agent.Open{For: 30 * time.Minute})
	e.H.Advance(time.Hour)
	e.open(agent.Open{})
	closed = e.H.Events("routing.shadowrocket_agent_session_closed")
	if last := closed[len(closed)-1]; last.Payload["reason"] != "expired" || last.Payload["session"] != float64(third.SessionID) {
		t.Fatalf("a hand-off after an unrecorded expiry: %+v", last)
	}
}

func TestAgentReadsSecretsMasked(t *testing.T) {
	e := newEnv(t)
	op := e.open(agent.Open{})
	rec := e.get(op.Token, "/base")
	status(t, rec, http.StatusOK)
	body := rec.Body.String()
	noLeak(t, "GET /base", body)
	if rec.Header().Get("ETag") != `"1"` || !strings.Contains(body, "Home = "+agent.Masked+"\n") || !strings.Contains(body, "ca-p12 = "+agent.Masked+"\n") ||
		!strings.Contains(body, "hostname = *.example.com\n") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("GET /base: %v\n%s", rec.Header(), body)
	}

	rec = e.get(op.Token, "/context")
	status(t, rec, http.StatusOK)
	noLeak(t, "GET /context", rec.Body.String())
	var ctx struct {
		Problem string `json:"problem"`
		Config  struct {
			Name        string `json:"name"`
			File        string `json:"file"`
			RoutingList string `json:"routing_list"`
			Policy      string `json:"rule_policy"`
			Rules       int    `json:"rules_inserted"`
			Version     int    `json:"base_version"`
		} `json:"config"`
		Masked string    `json:"masked_value"`
		Check  checkJSON `json:"check"`
	}
	decode(t, rec, &ctx)
	if ctx.Config.File != "iphone.conf" || ctx.Config.RoutingList != "Main" || ctx.Config.Policy != "PROXY" || ctx.Config.Rules != 2 || ctx.Config.Version != 1 ||
		ctx.Masked != agent.Masked || !strings.Contains(ctx.Problem, "fix it") {
		t.Errorf("context: %+v", ctx)
	}
	if c := ctx.Check; !c.OK || c.RuleSection != 7 || c.Final != 9 || c.InsertAt != "before line 9" || len(c.Hidden) != 3 || c.Hidden[0].Key != "Home" || c.Hidden[0].Line != 5 {
		t.Errorf("check: %+v", c)
	}
	if strings.Contains(rec.Body.String(), "/r/") {
		t.Error("the context holds the config's URL")
	}

	rec = e.get(op.Token, "/output")
	status(t, rec, http.StatusOK)
	noLeak(t, "GET /output", rec.Body.String())
	if out := rec.Body.String(); !strings.Contains(out, "DOMAIN-SUFFIX,anthropic.com,PROXY\n") || !strings.Contains(out, "Home = "+agent.Masked) {
		t.Errorf("output:\n%s", out)
	}
}

func TestAgentSaveNeedsTheETagAndKeepsSecrets(t *testing.T) {
	e := newEnv(t)
	op := e.open(agent.Open{})
	masked := e.get(op.Token, "/base").Body.String()
	edited := strings.Replace(masked, "FINAL,DIRECT", "DOMAIN-SUFFIX,example.org,DIRECT\nFINAL,DIRECT", 1)

	status(t, e.put(op.Token, "", edited), http.StatusPreconditionRequired)
	status(t, e.put(op.Token, `"7"`, edited), http.StatusPreconditionFailed)
	if v := e.current(); v.Number != 1 {
		t.Fatalf("a refused save made v%d", v.Number)
	}

	rec := e.do(req{Method: http.MethodPut, Path: "/base?note=" + url.QueryEscape("allow example.org"), Token: op.Token, Header: map[string]string{"If-Match": `"1"`}, Body: edited})
	status(t, rec, http.StatusOK)
	noLeak(t, "the save's answer", rec.Body.String())
	var ans struct {
		Version int       `json:"version"`
		ETag    string    `json:"etag"`
		Changed bool      `json:"changed"`
		Check   checkJSON `json:"check"`
	}
	decode(t, rec, &ans)
	if ans.Version != 2 || ans.ETag != `"2"` || rec.Header().Get("ETag") != `"2"` || !ans.Changed || !ans.Check.OK {
		t.Errorf("answer: %+v", ans)
	}
	v := e.current()
	if want := strings.Replace(base, "FINAL,DIRECT", "DOMAIN-SUFFIX,example.org,DIRECT\nFINAL,DIRECT", 1); v.Number != 2 || v.Content != want || v.Note != "allow example.org" {
		t.Errorf("v2: %+v", v)
	}
	ev := e.H.Events("routing.shadowrocket_updated")
	if last := ev[len(ev)-1]; last.Actor != events.AgentActor(op.SessionID) || last.Payload["changes"] != "base" || last.Payload["version"] != 2.0 {
		t.Errorf("updated: %+v", last)
	}
	// the phone gets it
	out, _ := e.H.Mod.Shadowrocket.Output(bg, e.ID, access.All)
	if !strings.Contains(string(out), "DOMAIN-SUFFIX,example.org,DIRECT") || !strings.Contains(string(out), "pw-home-1") {
		t.Errorf("output after the save:\n%s", out)
	}
	// the same text again: no version, no event, not counted
	before := len(ev)
	rec = e.put(op.Token, `"2"`, edited)
	status(t, rec, http.StatusOK)
	decode(t, rec, &ans)
	if ans.Changed || ans.Version != 2 || len(e.H.Events("routing.shadowrocket_updated")) != before {
		t.Errorf("an unchanged save: %+v", ans)
	}
	// a save without a note says who made it
	status(t, e.put(op.Token, `"2"`, strings.Replace(edited, "example.org", "example.net", 1)), http.StatusOK)
	if v := e.current(); v.Note != "saved by an agent" {
		t.Errorf("note: %q", v.Note)
	}
	sess, err := e.H.Mod.ShadowrocketAgent.Current(bg, e.ID)
	if err != nil || sess.Saves != 2 {
		t.Errorf("saves: %d %v", sess.Saves, err)
	}
}

func TestAgentSaveRefusals(t *testing.T) {
	e := newEnv(t)
	op := e.open(agent.Open{})
	masked := e.get(op.Token, "/base").Body.String()
	for name, c := range map[string]struct {
		body string
		code int
	}{
		"renamed proxy":  {strings.Replace(masked, "Home = ", "House = ", 1), http.StatusUnprocessableEntity},
		"mask on a rule": {strings.Replace(masked, "FINAL,DIRECT", "FINAL,"+agent.Masked, 1), http.StatusUnprocessableEntity},
		"no [Rule]":      {strings.Replace(masked, "[Rule]", "[Rules]", 1), http.StatusUnprocessableEntity},
		"not UTF-8":      {masked + "\xff\xfe", http.StatusUnprocessableEntity},
		"over 256 KiB":   {masked + "#" + strings.Repeat("x", 256<<10) + "\n", http.StatusUnprocessableEntity},
		"over 512 KiB":   {strings.Repeat("x", 513<<10), http.StatusRequestEntityTooLarge},
		"empty":          {"", http.StatusUnprocessableEntity},
	} {
		rec := e.put(op.Token, `"1"`, c.body)
		if rec.Code != c.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	rec := e.do(req{Method: http.MethodPut, Path: "/base?note=" + strings.Repeat("n", 201), Token: op.Token, Header: map[string]string{"If-Match": `"1"`}, Body: masked + "# x\n"})
	status(t, rec, http.StatusUnprocessableEntity)
	if v := e.current(); v.Number != 1 || v.Content != base {
		t.Errorf("refused saves changed the base: v%d", v.Number)
	}
	rec = e.put(op.Token, `"1"`, strings.Replace(masked, "[Rule]", "[Rules]", 1))
	if !strings.Contains(rec.Body.String(), "no [Rule] section") {
		t.Errorf("no rule: %s", rec.Body.String())
	}
}

func TestAgentSaveKeepsCRLF(t *testing.T) {
	e := newEnv(t)
	crlf := strings.ReplaceAll(base, "\n", "\r\n")
	if _, _, err := e.H.Mod.Shadowrocket.SaveBase(bg, e.ID, access.All, 0, crlf, "", "admin"); err != nil {
		t.Fatal(err)
	}
	op := e.open(agent.Open{})
	lf := strings.ReplaceAll(e.get(op.Token, "/base").Body.String(), "\r\n", "\n")
	status(t, e.put(op.Token, `"2"`, strings.Replace(lf, "bypass-system = true", "bypass-system = false", 1)), http.StatusOK)
	if v := e.current(); v.Content != strings.Replace(crlf, "bypass-system = true", "bypass-system = false", 1) {
		t.Errorf("CRLF lost: %q", v.Content)
	}
}

func TestAgentAndMemberEditTogether(t *testing.T) {
	e := newEnv(t)
	op := e.open(agent.Open{})
	masked := e.get(op.Token, "/base").Body.String()
	// the member saves first: the agent's write with the old ETag is refused
	if _, _, err := e.H.Mod.Shadowrocket.SaveBase(bg, e.ID, access.All, 1, base+"# member\n", "", events.MemberActor(1)); err != nil {
		t.Fatal(err)
	}
	status(t, e.put(op.Token, `"1"`, masked+"# agent\n"), http.StatusPreconditionFailed)
	if v := e.current(); v.Number != 2 || !strings.HasSuffix(v.Content, "# member\n") {
		t.Errorf("after the refused write: %+v", v)
	}
}

func TestCheckAnUnsavedBody(t *testing.T) {
	e := newEnv(t)
	op := e.open(agent.Open{})
	noFinal := "[General]\n\n[Rule]\nDOMAIN,example.com,PROXY\n\n[Host]\nexample.com = 1.2.3.4\n"
	rec := e.do(req{Method: http.MethodPost, Path: "/base/check", Token: op.Token, Body: noFinal})
	status(t, rec, http.StatusOK)
	var ans struct {
		Saved bool      `json:"saved"`
		Check checkJSON `json:"check"`
	}
	decode(t, rec, &ans)
	if ans.Saved || !ans.Check.OK || ans.Check.RuleSection != 3 || ans.Check.Final != 0 || ans.Check.InsertAt != "before line 5, followed by FINAL,DIRECT" {
		t.Errorf("no FINAL: %+v", ans)
	}
	rec = e.do(req{Method: http.MethodPost, Path: "/base/check", Token: op.Token, Body: "[General]\n"})
	decode(t, rec, &ans)
	if ans.Check.OK || len(ans.Check.Errors) != 1 || !strings.Contains(ans.Check.Errors[0], "no [Rule] section") || ans.Check.InsertAt != "nowhere: there is no [Rule] section" {
		t.Errorf("no rule: %+v", ans)
	}
	rec = e.do(req{Method: http.MethodPost, Path: "/base/check", Token: op.Token})
	decode(t, rec, &ans)
	if !ans.Saved || !ans.Check.OK || len(ans.Check.Hidden) != 3 {
		t.Errorf("the saved base: %+v", ans)
	}
	if v := e.current(); v.Number != 1 {
		t.Errorf("a check saved v%d", v.Number)
	}
}

func TestRevokeAndDeleteEndTheSession(t *testing.T) {
	e := newEnv(t)
	ag := e.H.Mod.ShadowrocketAgent
	op := e.open(agent.Open{})
	other, err := e.H.Mod.Shadowrocket.Create(bg, routingtest.Owner, shadowrocket.New{Name: "ipad", ListID: 1, Policy: "PROXY", Base: base}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := ag.Revoke(bg, other, op.SessionID, "admin"); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("revoking through another config: %v", err)
	}
	status(t, e.get(op.Token, "/base"), http.StatusOK)
	if err := ag.Revoke(bg, e.ID, op.SessionID, events.MemberActor(1)); err != nil {
		t.Fatal(err)
	}
	status(t, e.get(op.Token, "/base"), http.StatusUnauthorized)
	if err := ag.Revoke(bg, e.ID, op.SessionID, "admin"); !errors.Is(err, agent.ErrNotOpen) {
		t.Errorf("revoking twice: %v", err)
	}
	closed := e.H.Events("routing.shadowrocket_agent_session_closed")
	if len(closed) != 1 || closed[0].Payload["reason"] != "revoked" || closed[0].Actor != events.MemberActor(1) {
		t.Errorf("closed: %+v", closed)
	}

	op = e.open(agent.Open{})
	if err := e.H.Mod.Shadowrocket.Delete(bg, e.ID, access.All, "admin"); err != nil {
		t.Fatal(err)
	}
	status(t, e.get(op.Token, "/base"), http.StatusUnauthorized)
}

func TestAgentRateLimits(t *testing.T) {
	e := newEnv(t)
	op := e.open(agent.Open{})
	for range 60 {
		status(t, e.get(op.Token, "/base"), http.StatusOK)
	}
	rec := e.get(op.Token, "/base")
	status(t, rec, http.StatusTooManyRequests)
	if ra, _ := strconv.Atoi(rec.Header().Get("Retry-After")); ra < 1 || ra > 61 {
		t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	e.H.Advance(time.Minute)
	status(t, e.get(op.Token, "/base"), http.StatusOK)
	e.H.Exec(`UPDATE routing_shadowrocket_agent_sessions SET requests = 600 WHERE id = ?`, op.SessionID)
	status(t, e.get(op.Token, "/base"), http.StatusTooManyRequests)
}

func TestExpirySchedule(t *testing.T) {
	e := newEnv(t)
	e.open(agent.Open{For: 30 * time.Minute})
	e.H.Advance(31 * time.Minute)
	e.H.StartJobs()
	e.H.RunSchedule(agent.JobType)
	e.H.Drain()
	closed := e.H.Events("routing.shadowrocket_agent_session_closed")
	if len(closed) != 1 || closed[0].Payload["reason"] != "expired" || !strings.HasPrefix(closed[0].Actor, "job:") {
		t.Errorf("closed: %+v", closed)
	}
}
