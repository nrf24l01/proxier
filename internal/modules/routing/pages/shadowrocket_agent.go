package pages

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
	"github.com/tikhonp/proxier/internal/modules/routing/shadowrocket"
	sragent "github.com/tikhonp/proxier/internal/modules/routing/shadowrocket/agent"
	"github.com/tikhonp/proxier/internal/modules/routing/store"
	"github.com/tikhonp/proxier/internal/platform/agentapi"
	"github.com/tikhonp/proxier/internal/platform/i18n"
	"github.com/tikhonp/proxier/internal/platform/web"
)

func srAgentHref(id int64) string { return srHref(id) + "/agent" }

// ---- the band ---------------------------------------------------------------

// srAgentBandView is the open session as the config page and the base
// editor show it.
type srAgentBandView struct {
	Config    int64
	ID        int64
	Agent     string
	Problem   string // its first line
	Saves     int
	ExpiresAt time.Time
	CanRevoke bool
}

// srAgentBand returns the config's open agent session, nil when there is none.
func (h *handler) srAgentBand(c *echo.Context, configID int64) (*srAgentBandView, error) {
	ctx := c.Request().Context()
	sess, err := h.ShadowrocketAgent.Current(ctx, configID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cfg, err := h.Shadowrocket.Get(ctx, configID, scope(ctx))
	if err != nil {
		return nil, err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(sess.Problem), "\n")
	if r := []rune(first); len(r) > 120 {
		first = string(r[:120]) + "…"
	}
	return &srAgentBandView{Config: configID, ID: sess.ID, Agent: sess.Agent, Problem: first, Saves: sess.Saves, ExpiresAt: sess.ExpiresAt.Time,
		CanRevoke: scope(ctx).Writes(cfg.OwnerID)}, nil
}

// ---- the hand-off form ------------------------------------------------------

type srAgentFormView struct {
	C       shadowrocket.Config
	Version int
	Hidden  string // the values the agent reads masked
	FormID  string

	// as typed
	Problem string
	Agent   string
	For     string // minutes: 30 120 480

	Error string // translated
}

func (h *handler) renderSRAgentForm(c *echo.Context, status int, v srAgentFormView) error {
	ctx := c.Request().Context()
	cur, err := h.Shadowrocket.Version(ctx, v.C.ID, 0)
	if err != nil {
		return err
	}
	var hidden []string
	for _, sp := range sragent.Spots(cur.Content) {
		hidden = append(hidden, "["+sp.Section+"] "+sp.Key)
	}
	v.Version, v.Hidden, v.FormID = cur.Number, strings.Join(hidden, ", "), agentapi.NewFormID()
	return web.Render(c, status, srAgentFormPage(h.shell(c, i18n.T(ctx, "shadowrocket.agent.title"), srPath), v))
}

func (h *handler) srAgentForm(c *echo.Context) error {
	cfg, err := h.loadSRToChange(c)
	if err != nil {
		return err
	}
	return h.renderSRAgentForm(c, http.StatusOK, srAgentFormView{C: cfg, Agent: "claude-code", For: "120"})
}

// ---- opening ----------------------------------------------------------------

type srAgentResultView struct {
	C         shadowrocket.Config
	Masked    string
	Prompt    string // the real one, for the copy button
	Size      string
	ExpiresAt time.Time
	Repeated  bool // the form was posted before: the prompt is not shown again
}

func (h *handler) srAgentOpen(c *echo.Context) error {
	cfg, err := h.loadSRToChange(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	title := i18n.T(ctx, "shadowrocket.agent.title")
	if fid := c.FormValue("form_id"); fid != "" && !h.forms.Take(fid) {
		return web.Render(c, http.StatusOK, srAgentResultPage(h.shell(c, title, srPath), srAgentResultView{C: cfg, Repeated: true}))
	}
	v := srAgentFormView{C: cfg, Problem: c.FormValue("problem"), Agent: c.FormValue("agent"), For: c.FormValue("for")}
	opened, err := h.ShadowrocketAgent.OpenSession(ctx, sragent.Open{ConfigID: cfg.ID, Problem: v.Problem, Agent: v.Agent, For: agentapi.FormDuration(v.For)}, web.Actor(ctx))
	switch {
	case errors.Is(err, sragent.ErrNoProblem):
		v.Error = i18n.T(ctx, "agent.err.problem")
	case errors.Is(err, sragent.ErrBadAgent):
		v.Error = i18n.T(ctx, "agent.err.agent")
	case errors.Is(err, sragent.ErrNotFound):
		return echo.ErrNotFound
	case err != nil:
		return err
	}
	if v.Error != "" {
		return h.renderSRAgentForm(c, http.StatusUnprocessableEntity, v)
	}
	// Whatever the browser does with this answer, the prompt is not cached.
	c.Response().Header().Set("Cache-Control", "no-store")
	return web.Render(c, http.StatusOK, srAgentResultPage(h.shell(c, title, srPath), srAgentResultView{
		C: cfg, Masked: agentapi.Mask(opened.Prompt, opened.Token), Prompt: opened.Prompt, ExpiresAt: opened.ExpiresAt,
		Size: fmt.Sprintf("%.1f KB", float64(len(opened.Prompt))/1024),
	}))
}

// ---- revoke, polling --------------------------------------------------------

func (h *handler) srAgentRevoke(c *echo.Context) error {
	cfg, err := h.loadSRToChange(c)
	if err != nil {
		return err
	}
	sid, err := strconv.ParseInt(c.Param("sid"), 10, 64)
	if err != nil {
		return echo.ErrNotFound
	}
	// A session that already ended is as revoked as it can be.
	err = h.ShadowrocketAgent.Revoke(c.Request().Context(), cfg.ID, sid, web.Actor(c.Request().Context()))
	switch {
	case errors.Is(err, sragent.ErrNotFound):
		return echo.ErrNotFound
	case err != nil && !errors.Is(err, sragent.ErrNotOpen):
		return err
	}
	return web.Redirect(c, srHref(cfg.ID)+"?revoked=1")
}

// srBaseVersion answers the base editor's poll while an agent session is
// open: the same poller when the base is still the version the editor
// opened, the "base changed" band when it is not. The editor sends that
// version as ?version=.
func (h *handler) srBaseVersion(c *echo.Context) error {
	cfg, err := h.loadSRToChange(c)
	if err != nil {
		return err
	}
	have, _ := strconv.Atoi(c.QueryParam("version"))
	if cfg.Version != have {
		return web.Render(c, http.StatusOK, srBaseChangedBand(cfg.ID))
	}
	if _, err := h.ShadowrocketAgent.Current(c.Request().Context(), cfg.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return web.Render(c, http.StatusOK, templ.NopComponent) // the session ended: stop asking
		}
		return err
	}
	return web.Render(c, http.StatusOK, srBasePoller(cfg.ID))
}
