package deploy

import (
	"context"
	"net/netip"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/tikhonp/proxier/internal/modules/servers/provision"
	"github.com/tikhonp/proxier/internal/modules/servers/render"
	"github.com/tikhonp/proxier/internal/modules/servers/store"
	"github.com/tikhonp/proxier/internal/platform/db"
	"github.com/tikhonp/proxier/internal/platform/events"
	"github.com/tikhonp/proxier/internal/platform/i18n"
)

// Addresses lists a server's additional addresses in the order templates see
// them.
func (s *Service) Addresses(ctx context.Context, serverID int64) ([]store.Address, error) {
	return store.ServerAddresses(ctx, s.DB.R, serverID)
}

// AddAddress appends an additional address to an active server. Proxier does
// not write its DNS record: the member manages it. The caller redeploys the
// server afterwards.
func (s *Service) AddAddress(ctx context.Context, serverID int64, ip, hostname, actor string) error {
	srv, err := s.activeServer(ctx, s.DB.R, serverID)
	if err != nil {
		return err
	}
	ip = strings.TrimSpace(ip)
	addr, err := netip.ParseAddr(ip)
	switch {
	case err != nil || !addr.Is4() || addr.Is4In6():
		return FieldErrors{"ip": {Key: "servers.err.ip_v4"}}
	}
	ip = addr.String()
	hostname = provision.NormalizeHostname(hostname)
	switch {
	case hostname == "":
		return FieldErrors{"hostname": {Key: "servers.err.hostname_required"}}
	case render.ValidHostname(hostname) != nil:
		return FieldErrors{"hostname": {Key: "servers.err.hostname_invalid"}}
	}
	// The server's own address and hostname are not additional ones.
	if ip == srv.IP {
		return FieldErrors{"ip": {Key: "servers.err.addr_ip_dup"}}
	}
	if hostname == srv.ManagementHostname || hostname == srv.ProxyHostname {
		return FieldErrors{"hostname": {Key: "servers.err.addr_host_dup"}}
	}
	if name, taken, err := store.HolderOfIP(ctx, s.DB.R, ip); err != nil {
		return FieldErrors{"ip": {Key: "servers.err.addr_check", Args: i18n.Args{"error": err.Error()}}}
	} else if taken {
		return FieldErrors{"ip": {Key: "servers.err.addr_ip_used", Args: i18n.Args{"name": name}}}
	}
	if name, taken, err := store.HolderOfHostname(ctx, s.DB.R, hostname); err != nil {
		return FieldErrors{"hostname": {Key: "servers.err.addr_check", Args: i18n.Args{"error": err.Error()}}}
	} else if taken {
		return FieldErrors{"hostname": {Key: "servers.err.addr_host_used", Args: i18n.Args{"name": name}}}
	}
	return s.DB.Write(ctx, func(tx *sqlx.Tx) error {
		if _, err := store.InsertAddress(ctx, tx, srv.ID, ip, hostname, db.At(s.now())); err != nil {
			return err
		}
		_, err := s.Events.Record(ctx, tx, events.Event{
			Type: "server.address_added", Subject: store.ServerSubject(srv.ID), Actor: actor,
			Payload: map[string]any{"ip": ip, "hostname": hostname},
		})
		return err
	})
}

// RemoveAddress deletes an additional address of an active server. The
// caller redeploys it afterwards; the member's DNS record is untouched.
func (s *Service) RemoveAddress(ctx context.Context, serverID, addressID int64, actor string) error {
	srv, err := s.activeServer(ctx, s.DB.R, serverID)
	if err != nil {
		return err
	}
	addr, err := store.GetAddress(ctx, s.DB.R, serverID, addressID)
	if err != nil {
		return err
	}
	addrs, err := store.ServerAddresses(ctx, s.DB.R, serverID)
	if err != nil {
		return err
	}
	// Endpoints bind to an address by its position, so positions never shift:
	// only the last one can go, and the next add takes its number.
	if len(addrs) > 0 && addr.Position != addrs[len(addrs)-1].Position {
		return FieldErrors{"address": {Key: "servers.err.addr_remove_last"}}
	}
	return s.DB.Write(ctx, func(tx *sqlx.Tx) error {
		ok, err := store.DeleteAddress(ctx, tx, serverID, addressID)
		if err != nil {
			return err
		}
		if !ok {
			return store.ErrNotFound
		}
		_, err = s.Events.Record(ctx, tx, events.Event{
			Type: "server.address_removed", Subject: store.ServerSubject(srv.ID), Actor: actor,
			Payload: map[string]any{"ip": addr.IP, "hostname": addr.Hostname},
		})
		return err
	})
}
