package agent

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
	"github.com/tikhonp/proxier/internal/modules/routing/shadowrocket"
	"github.com/tikhonp/proxier/internal/modules/routing/store"
	"github.com/tikhonp/proxier/internal/platform/access"
	"github.com/tikhonp/proxier/internal/platform/agentapi"
	"github.com/tikhonp/proxier/internal/platform/events"
)

// APIPrefix is where the API lives; the other agent APIs have the rest of /agent/v1.
const APIPrefix = "/agent/v1/shadowrocket"

const (
	sessionKey = "srAgentSession"
	// maxBody is the largest request body: one base, plus room for the line
	// endings readBody may change.
	maxBody = 2 * shadowrocket.MaxBase
	// defaultNote is the note of a version an agent saved without one.
	defaultNote = "saved by an agent"
)

var errSessionEnded = errors.New(agentapi.SessionEnded)

// Routes adds the agent API to g, which must be the public group: the API
// never reads or sets a cookie and answers to a bearer token only.
func (s *Service) Routes(g *echo.Group) {
	a := g.Group(APIPrefix, s.auth)
	a.GET("/context", s.getContext)
	a.GET("/base", s.getBase)
	a.PUT("/base", s.putBase)
	a.POST("/base/check", s.checkBase)
	a.GET("/output", s.getOutput)
}

// auth finds the session of the bearer token and counts the request against
// its limits in one short write. Every refusal to authenticate is the same
// 401: an ended, expired and unknown token cannot be told apart.
func (s *Service) auth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		r := c.Request()
		tok := agentapi.Bearer(r)
		if tok == "" {
			return agentapi.Ended(c)
		}
		if r.ContentLength > maxBody {
			return agentapi.Fail(c, http.StatusRequestEntityTooLarge, "the request body is too large")
		}
		r.Body = http.MaxBytesReader(c.Response(), r.Body, maxBody)

		var sess store.AgentSession
		var status int
		var retry time.Duration
		now := s.now()
		err := s.DB.Write(r.Context(), func(tx *sqlx.Tx) error {
			var err error
			sess, err = store.AgentSessionByToken(r.Context(), tx, s.Vault.Lookup(tok))
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusUnauthorized
				return nil
			}
			if err != nil {
				return err
			}
			if !sess.Open() || !sess.ExpiresAt.After(now.Time) {
				status = http.StatusUnauthorized
				return nil
			}
			rate, wait, ok := agentapi.Rate{Requests: sess.Requests, MinuteStart: sess.MinuteStart, MinuteCount: sess.MinuteCount}.Count(now, sess.ExpiresAt)
			if !ok {
				status, retry = http.StatusTooManyRequests, wait
				return nil
			}
			sess.Requests = rate.Requests
			return store.SetAgentRate(r.Context(), tx, sess.ID, rate.Requests, rate.MinuteStart, rate.MinuteCount)
		})
		if err != nil {
			s.Log.Error("shadowrocket agent: counting a request", "error", err)
			return agentapi.Fail(c, http.StatusInternalServerError, "internal error")
		}
		switch status {
		case http.StatusUnauthorized:
			return agentapi.Ended(c)
		case http.StatusTooManyRequests:
			return agentapi.TooMany(c, retry)
		}
		c.Set(sessionKey, sess)
		return next(c)
	}
}

func session(c *echo.Context) store.AgentSession { return c.Get(sessionKey).(store.AgentSession) }

// stillOpen runs in the save's transaction: it refuses (errSessionEnded) when
// the session was revoked, replaced or ran out since the request was let in,
// so an ended token never changes the base.
func (s *Service) stillOpen(c *echo.Context, tx *sqlx.Tx, sess store.AgentSession) error {
	cur, err := store.AgentSessionByID(c.Request().Context(), tx, sess.ID)
	if err != nil {
		return err
	}
	if !cur.Open() || !cur.ExpiresAt.After(s.Now()) {
		return errSessionEnded
	}
	return nil
}

func etag(version int) string { return `"` + strconv.Itoa(version) + `"` }

// current reads the session's config and its current base. A config deleted
// meanwhile took the session with it: the token is as ended as any other.
func (s *Service) current(c *echo.Context) (shadowrocket.Config, shadowrocket.Version, error) {
	ctx := c.Request().Context()
	id := session(c).ConfigID
	cfg, err := s.Shadowrocket.Get(ctx, id, access.All)
	if err != nil {
		return cfg, shadowrocket.Version{}, err
	}
	v, err := s.Shadowrocket.Version(ctx, id, 0)
	return cfg, v, err
}

func (s *Service) gone(c *echo.Context, err error) error {
	if errors.Is(err, shadowrocket.ErrNotFound) || errors.Is(err, shadowrocket.ErrNoVersion) {
		return agentapi.Ended(c)
	}
	s.Log.Error("shadowrocket agent: reading the config", "error", err)
	return agentapi.Fail(c, http.StatusInternalServerError, "internal error")
}

// ---- the check --------------------------------------------------------------

// checkJSON is what a save would say about a base, and where Proxier puts
// its rules in it.
type checkJSON struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors"`
	Bytes  int      `json:"bytes"`
	Lines  int      `json:"lines"`
	// Lines from 1; 0 when there is none.
	RuleSection int    `json:"rule_section_line"`
	Final       int    `json:"final_line"`
	InsertAt    string `json:"rules_inserted"`
	Hidden      []Spot `json:"hidden"`
}

// check reports on a base: the refusals a save would give, read from the
// real base, and where the rules go, read from the masked one (the same
// lines, so the numbers hold for what the agent reads).
func check(body string) checkJSON {
	out := checkJSON{Errors: []string{}, Hidden: Spots(body), Bytes: len(body), Lines: strings.Count(body, "\n")}
	if body != "" && !strings.HasSuffix(body, "\n") {
		out.Lines++
	}
	if out.Hidden == nil {
		out.Hidden = []Spot{}
	}
	if err := shadowrocket.Validate([]byte(body)); err != nil {
		out.Errors = append(out.Errors, strings.TrimPrefix(err.Error(), "shadowrocket: "))
	}
	if body == "" {
		out.Errors = append(out.Errors, "the base config is empty")
	}
	out.OK = len(out.Errors) == 0
	p := shadowrocket.Where([]byte(maskBody(body)))
	out.RuleSection, out.Final = p.Rule, p.Final
	switch {
	case p.Rule == 0:
		out.InsertAt = "nowhere: there is no [Rule] section"
	case p.Before == 0:
		out.InsertAt = "after the last line"
	default:
		out.InsertAt = "before line " + strconv.Itoa(p.Before)
	}
	if p.Rule > 0 && p.Final == 0 {
		out.InsertAt += ", followed by FINAL,DIRECT"
	}
	return out
}

// ---- reading ----------------------------------------------------------------

type contextJSON struct {
	Problem string `json:"problem"`
	Agent   string `json:"agent"`
	Config  struct {
		Name        string `json:"name"`
		File        string `json:"file"`
		RoutingList string `json:"routing_list"`
		Policy      string `json:"rule_policy"`
		Rules       int    `json:"rules_inserted"`
		Enabled     bool   `json:"enabled"`
		Version     int    `json:"base_version"`
	} `json:"config"`
	ExpiresAt time.Time `json:"expires_at"`
	Masked    string    `json:"masked_value"`
	Check     checkJSON `json:"check"`
	Links     struct {
		Base   string `json:"base"`
		Check  string `json:"check"`
		Output string `json:"output"`
	} `json:"links"`
}

func (s *Service) getContext(c *echo.Context) error {
	sess := session(c)
	cfg, v, err := s.current(c)
	if err != nil {
		return s.gone(c, err)
	}
	out := contextJSON{Problem: sess.Problem, Agent: sess.Agent, ExpiresAt: sess.ExpiresAt.Time, Masked: Masked, Check: check(v.Content)}
	out.Config.Name, out.Config.File, out.Config.RoutingList, out.Config.Policy = cfg.Name, cfg.File(), cfg.List, cfg.Policy
	out.Config.Rules, out.Config.Enabled, out.Config.Version = cfg.Rules, cfg.Enabled, v.Number
	out.Links.Base, out.Links.Check, out.Links.Output = APIPrefix+"/base", APIPrefix+"/base/check", APIPrefix+"/output"
	return c.JSON(http.StatusOK, out)
}

func (s *Service) getBase(c *echo.Context) error {
	_, v, err := s.current(c)
	if err != nil {
		return s.gone(c, err)
	}
	c.Response().Header().Set("ETag", etag(v.Number))
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", []byte(maskBody(v.Content)))
}

func (s *Service) getOutput(c *echo.Context) error {
	_, v, err := s.current(c)
	if err != nil {
		return s.gone(c, err)
	}
	out, err := s.Shadowrocket.OutputOf(c.Request().Context(), session(c).ConfigID, maskBody(v.Content))
	if err != nil {
		return s.gone(c, err)
	}
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", out)
}

// readBody reads the request's base with the stored base's line endings and
// its hidden values put back. code is 0 when it worked.
func readBody(c *echo.Context, stored string) (string, int, string) {
	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return "", http.StatusRequestEntityTooLarge, "the request body is too large"
		}
		return "", http.StatusBadRequest, "could not read the body"
	}
	body, err := unmask(lineEndings(string(raw), stored), stored)
	if err != nil {
		return "", http.StatusUnprocessableEntity, err.Error() + ": keep " + Masked + " only where the base you read had it, under the same key in the same section"
	}
	return body, 0, ""
}

// lineEndings gives an LF-only body the stored base's CRLF endings: an agent
// writes LF, and the inserted rules follow the base's first line ending.
func lineEndings(body, stored string) string {
	first, _, _ := strings.Cut(stored, "\n")
	if !strings.HasSuffix(first, "\r") || strings.Contains(body, "\r") {
		return body
	}
	return strings.ReplaceAll(body, "\n", "\r\n")
}

// ---- writing ----------------------------------------------------------------

func (s *Service) putBase(c *echo.Context) error {
	ctx := c.Request().Context()
	sess := session(c)
	_, v, err := s.current(c)
	if err != nil {
		return s.gone(c, err)
	}
	switch m := c.Request().Header.Get("If-Match"); {
	case m == "":
		return agentapi.Fail(c, http.StatusPreconditionRequired, "send If-Match with the base's ETag")
	case !agentapi.Matches(m, etag(v.Number)):
		return agentapi.Fail(c, http.StatusPreconditionFailed, "the base config changed")
	}
	body, code, msg := readBody(c, v.Content)
	if code != 0 {
		return agentapi.Fail(c, code, msg)
	}
	if body == "" {
		return agentapi.Fail(c, http.StatusUnprocessableEntity, "the base config is empty")
	}
	note := strings.TrimSpace(c.QueryParam("note"))
	if note == "" {
		note = defaultNote
	}
	if utf8.RuneCountInString(note) > shadowrocket.MaxNote {
		return agentapi.Fail(c, http.StatusUnprocessableEntity, "the note is over 200 characters")
	}
	changed := false
	n, _, err := s.Shadowrocket.SaveBaseIn(ctx, sess.ConfigID, access.All, v.Number, body, note, events.AgentActor(sess.ID), func(tx *sqlx.Tx, ch bool) error {
		if err := s.stillOpen(c, tx, sess); err != nil {
			return err
		}
		changed = ch
		if ch {
			return store.AddAgentSave(ctx, tx, sess.ID)
		}
		return nil
	})
	var fe store.FieldErrors
	switch {
	case errors.Is(err, shadowrocket.ErrBaseChanged):
		return agentapi.Fail(c, http.StatusPreconditionFailed, "the base config changed")
	case errors.Is(err, errSessionEnded), errors.Is(err, shadowrocket.ErrNotFound):
		return agentapi.Ended(c)
	case errors.As(err, &fe):
		msg := "the base config is refused"
		if errs := check(body).Errors; len(errs) > 0 {
			msg = errs[0]
		}
		return agentapi.Fail(c, http.StatusUnprocessableEntity, msg)
	case err != nil:
		s.Log.Error("shadowrocket agent: saving the base", "error", err)
		return agentapi.Fail(c, http.StatusInternalServerError, "internal error")
	}
	c.Response().Header().Set("ETag", etag(n))
	return c.JSON(http.StatusOK, map[string]any{"version": n, "etag": etag(n), "changed": changed, "check": check(body)})
}

// checkBase checks the saved base, or the base in the request body without
// saving it.
func (s *Service) checkBase(c *echo.Context) error {
	_, v, err := s.current(c)
	if err != nil {
		return s.gone(c, err)
	}
	body, code, msg := readBody(c, v.Content)
	if code != 0 {
		return agentapi.Fail(c, code, msg)
	}
	saved := body == ""
	if saved {
		body = v.Content
	}
	return c.JSON(http.StatusOK, map[string]any{"version": v.Number, "saved": saved, "check": check(body)})
}
