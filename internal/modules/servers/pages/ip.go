package pages

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/tikhonp/proxier/internal/modules/servers/health"
	"github.com/tikhonp/proxier/internal/modules/servers/ipchange"
	"github.com/tikhonp/proxier/internal/modules/servers/store"
	"github.com/tikhonp/proxier/internal/platform/access"
	"github.com/tikhonp/proxier/internal/platform/db"
	"github.com/tikhonp/proxier/internal/platform/i18n"
	"github.com/tikhonp/proxier/internal/platform/ui"
	"github.com/tikhonp/proxier/internal/platform/web"
)

func (h *handler) registerIP(r web.Routes) {
	srv := r.Area(access.Servers)
	srv.Manage.GET("/servers/:id/ip", h.ipPage, h.operable)
	srv.Manage.POST("/servers/:id/ip", h.ipPost, h.operable)
	srv.Manage.POST("/servers/:id/previous/:pid/forget", h.forgetPrevious, h.operable)
}

// stackSample is an address no server has: rendering the stack at it tells
// whether the template uses .Server.IP at all.
const stackSample = "192.0.2.1"

type ipView struct {
	S    store.Server
	Flag string
	// DNS is the records to repoint, "name → current IP".
	DNS []string
	// Previous are the server's previous IPs, to switch back to.
	Previous []string
	// NamesIP: the stack uses the IP, so a redeploy follows the change.
	NamesIP     bool
	IP          string
	Keep, Force bool
	Errs        map[string]string
}

func (h *handler) ipView(ctx context.Context, s store.Server) (ipView, error) {
	v := ipView{S: s, Flag: flagOf(s), Keep: true}
	recs, err := store.DNSRecords(ctx, h.Store.DB.R, s.ID)
	if err != nil {
		return v, err
	}
	for _, r := range recs {
		v.DNS = append(v.DNS, r.Name+" → "+r.Content)
	}
	prevs, err := store.PreviousIPs(ctx, h.Store.DB.R, s.ID)
	if err != nil {
		return v, err
	}
	for _, p := range prevs {
		v.Previous = append(v.Previous, p.IP)
	}
	if names, err := h.Deploy.StackNamesIP(ctx, s.ID, stackSample); err != nil {
		h.Log.Warn("servers: does the stack name the IP", "server", s.Name, "error", err)
	} else {
		v.NamesIP = names
	}
	return v, nil
}

func (h *handler) renderIP(c *echo.Context, status int, v ipView) error {
	ctx := c.Request().Context()
	return web.Render(c, status, ipPageView(h.shell(c, i18n.T(ctx, "servers.ip.title", i18n.Args{"name": v.S.Name}), "/servers"), v))
}

// ipPage is the Change IP dialog; ?ip= fills the field (Switch back).
func (h *handler) ipPage(c *echo.Context) error {
	s, err := h.activeServer(c)
	if err != nil {
		return err
	}
	v, err := h.ipView(c.Request().Context(), s)
	if err != nil {
		return err
	}
	v.IP = c.QueryParam("ip")
	return h.renderIP(c, http.StatusOK, v)
}

func (h *handler) ipPost(c *echo.Context) error {
	s, err := h.activeServer(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	f := ipchange.Form{IP: c.FormValue("ip"), Keep: c.FormValue("keep") == "1", Force: c.FormValue("force") == "1"}
	job, err := h.IPChange.Change(ctx, s.ID, f, web.Actor(ctx))
	var fe ipchange.FieldErrors
	switch {
	case errors.As(err, &fe):
		v, verr := h.ipView(ctx, s)
		if verr != nil {
			return verr
		}
		v.IP, v.Keep, v.Force = f.IP, f.Keep, f.Force
		v.Errs = map[string]string{}
		for k, m := range fe {
			v.Errs[k] = i18n.T(ctx, m.Key, m.Args)
		}
		return h.renderIP(c, http.StatusUnprocessableEntity, v)
	case errors.Is(err, ipchange.ErrNotActive):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	case err != nil:
		return notFound(err)
	}
	return web.Redirect(c, serverHref(s.ID)+"?job="+i64(job))
}

func (h *handler) forgetPrevious(c *echo.Context) error {
	s, err := h.loadServer(c)
	if err != nil {
		return err
	}
	pid, err := strconv.ParseInt(c.Param("pid"), 10, 64)
	if err != nil {
		return echo.ErrNotFound
	}
	ctx := c.Request().Context()
	switch err := h.IPChange.Forget(ctx, s.ID, pid, web.Actor(ctx)); {
	case errors.Is(err, ipchange.ErrNotFound):
		return echo.ErrNotFound
	case errors.Is(err, ipchange.ErrNotActive):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	case err != nil:
		return err
	}
	return web.Redirect(c, serverHref(s.ID)+"/health")
}

// --------------------------------------------------- on the Health tab

type previousRow struct {
	ID         int64
	IP         string
	Kind, Word string
	Since      string // in this state since
	Left       string // when it stopped being the server's IP
	Detail     string
	When, At   string
	Strip      []string
}

// previousHref is where a previous IP's actions post.
func previousHref(serverID, id int64) string {
	return serverHref(serverID) + "/previous/" + strconv.FormatInt(id, 10)
}

// previousKind maps a previous IP's state to a marker kind.
func previousKind(state string) string {
	return map[string]string{store.PreviousWorks: "ok", store.PreviousFails: "blocked"}[state]
}

func (h *handler) previousRows(ctx context.Context, s store.Server, now time.Time) ([]previousRow, error) {
	loc := i18n.From(ctx)
	prevs, err := store.PreviousIPs(ctx, h.Store.DB.R, s.ID)
	if err != nil {
		return nil, err
	}
	var out []previousRow
	for _, p := range prevs {
		row := previousRow{
			ID: p.ID, IP: p.IP, Kind: previousKind(p.State), Word: i18n.T(ctx, "servers.previous.state."+p.State),
			Since: loc.Ago(p.StateSince.Time), Left: loc.Time(p.Since.Time),
		}
		if row.Kind == "" {
			row.Kind = "unknown"
		}
		last, ok, err := store.LatestPreviousResult(ctx, h.Store.DB.R, p.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			row.When, row.At = loc.Ago(last.At.Time), loc.Time(last.At.Time)
			d := health.ParsePreviousDetail(last.Detail)
			switch {
			case last.Inconclusive:
				row.Detail = i18n.T(ctx, "servers.previous.inconclusive")
			case !last.OK:
				row.Detail = d.FirstError()
			case len(d.Endpoints) > 0:
				row.Detail = i18n.T(ctx, "servers.previous.passed", i18n.Args{"first": d.Endpoints[0].FirstByteMS, "kbps": d.Endpoints[0].Kbps})
			}
		} else {
			row.Detail = i18n.T(ctx, "servers.previous.untested")
		}
		hist, err := store.PreviousResultsSince(ctx, h.Store.DB.R, p.ID, db.At(now.Add(-24*time.Hour)))
		if err != nil {
			return nil, err
		}
		rs := make([]store.CheckResult, len(hist))
		for i, r := range hist {
			rs[i] = store.CheckResult{At: r.At, OK: r.OK, Class: r.Class, Inconclusive: r.Inconclusive}
		}
		row.Strip = strip(rs, "", "", now, 48, 30*time.Minute)
		out = append(out, row)
	}
	return out, nil
}

// previousActions are a previous IP's buttons: switch back, forget.
func previousActions(ctx context.Context, s store.Server, p previousRow) (back, forget ui.Action) {
	back = ui.Action{ID: "server.previous.back." + strconv.FormatInt(p.ID, 10), Label: "servers.previous.back", Method: "GET",
		Href: serverHref(s.ID) + "/ip?ip=" + p.IP, Small: true}
	forget = ui.Action{
		ID: "server.previous.forget." + strconv.FormatInt(p.ID, 10), Label: "servers.previous.forget", Method: "POST",
		Href: previousHref(s.ID, p.ID) + "/forget", Small: true,
		Confirm: &ui.Confirm{Strength: 1, Title: i18n.T(ctx, "servers.previous.forget.title", i18n.Args{"ip": p.IP}),
			Body: i18n.T(ctx, "servers.previous.forget.body", i18n.Args{"name": s.Name})},
	}
	return back, forget
}
