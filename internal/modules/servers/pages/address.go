package pages

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/tikhonp/proxier/internal/modules/servers/deploy"
	"github.com/tikhonp/proxier/internal/modules/servers/store"
	"github.com/tikhonp/proxier/internal/platform/access"
	"github.com/tikhonp/proxier/internal/platform/i18n"
	"github.com/tikhonp/proxier/internal/platform/web"
)

func (h *handler) registerAddresses(r web.Routes) {
	srv := r.Area(access.Servers)
	srv.Manage.GET("/servers/:id/addresses", h.addressesPage, h.operable)
	srv.Manage.POST("/servers/:id/addresses", h.addressesPost, h.operable)
	srv.Manage.POST("/servers/:id/addresses/:aid/delete", h.addressDelete, h.operable)
}

type addressView struct {
	S         store.Server
	Addresses []store.Address
	IP, Host  string
	Errs      map[string]string
	Saved     bool
}

func (h *handler) addressView(ctx context.Context, s store.Server) (addressView, error) {
	addrs, err := h.Deploy.Addresses(ctx, s.ID)
	return addressView{S: s, Addresses: addrs}, err
}

func (h *handler) renderAddresses(c *echo.Context, status int, v addressView) error {
	ctx := c.Request().Context()
	return web.Render(c, status, serverAddressPage(h.shell(c, i18n.T(ctx, "servers.addr.title", i18n.Args{"name": v.S.Name}), "/servers"), v))
}

func (h *handler) addressesPage(c *echo.Context) error {
	s, err := h.activeServer(c)
	if err != nil {
		return err
	}
	v, err := h.addressView(c.Request().Context(), s)
	if err != nil {
		return err
	}
	v.Saved = c.QueryParam("saved") == "1"
	return h.renderAddresses(c, http.StatusOK, v)
}

func (h *handler) addressesPost(c *echo.Context) error {
	ctx := c.Request().Context()
	s, err := h.activeServer(c)
	if err != nil {
		return err
	}
	ip, host := c.FormValue("ip"), c.FormValue("hostname")
	if err := h.Deploy.AddAddress(ctx, s.ID, ip, host, web.Actor(ctx)); err != nil {
		var fe deploy.FieldErrors
		if errors.As(err, &fe) {
			v, verr := h.addressView(ctx, s)
			if verr != nil {
				return verr
			}
			v.IP, v.Host, v.Errs = ip, host, translate(c, fe)
			return h.renderAddresses(c, http.StatusUnprocessableEntity, v)
		}
		return err
	}
	return h.redeployAddresses(c, s.ID)
}

func (h *handler) addressDelete(c *echo.Context) error {
	ctx := c.Request().Context()
	s, err := h.activeServer(c)
	if err != nil {
		return err
	}
	aid, err := strconv.ParseInt(c.Param("aid"), 10, 64)
	if err != nil {
		return echo.ErrNotFound
	}
	if err := h.Deploy.RemoveAddress(ctx, s.ID, aid, web.Actor(ctx)); err != nil {
		var fe deploy.FieldErrors
		switch {
		case errors.Is(err, store.ErrNotFound):
			return echo.ErrNotFound
		case errors.As(err, &fe):
			if m, ok := fe["address"]; ok {
				return echo.NewHTTPError(http.StatusConflict, i18n.T(ctx, m.Key, m.Args))
			}
		}
		return err
	}
	return h.redeployAddresses(c, s.ID)
}

// redeployAddresses applies the addresses to the stack. A template that
// ignores addresses changes nothing, and that is fine.
func (h *handler) redeployAddresses(c *echo.Context, id int64) error {
	ctx := c.Request().Context()
	if _, err := h.Deploy.Apply(ctx, id, deploy.Target{Force: true}, "", web.Actor(ctx)); err != nil && !errors.Is(err, deploy.ErrNothingToChange) {
		return err
	}
	return web.Redirect(c, serverHref(id)+"/addresses?saved=1")
}
