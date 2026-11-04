package deploy

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/tikhonp/proxier/internal/modules/servers/manifest"
	"github.com/tikhonp/proxier/internal/modules/servers/store"
)

// CheckSetup is what health checks need of a server's template version: the
// stack's directory, the checks to run there and the proxy test object.
type CheckSetup struct {
	Server       store.Server
	Dir          string
	Checks       []manifest.Check
	ProxyTestURL string
	// Uninstall are the version's uninstall steps (retirement), rendered.
	Uninstall []manifest.Step
}

// CheckSetup renders the version in force for the server with its stored
// parameters. It writes nothing.
func (s *Service) CheckSetup(ctx context.Context, serverID int64) (CheckSetup, error) {
	srv, err := store.GetServer(ctx, s.DB.R, serverID)
	if err != nil {
		return CheckSetup{}, err
	}
	c, err := s.compute(ctx, srv, Target{}, nil)
	if err != nil {
		return CheckSetup{Server: srv}, err
	}
	return CheckSetup{Server: srv, Dir: c.Man.Dir, Checks: c.Rendered.Checks, ProxyTestURL: c.Rendered.ProxyTestURL, Uninstall: c.Rendered.Uninstall}, nil
}

// StackNamesIP says whether the version in force renders anything else for
// the server at ip than at its IP: its files, steps, endpoints or checks use
// .Server.IP, so an IP change needs a redeploy to reach the stack.
func (s *Service) StackNamesIP(ctx context.Context, serverID int64, ip string) (bool, error) {
	srv, err := store.GetServer(ctx, s.DB.R, serverID)
	if err != nil {
		return false, err
	}
	now, err := s.compute(ctx, srv, Target{}, nil)
	if err != nil {
		return false, err
	}
	moved := srv
	moved.IP = ip
	then, err := s.compute(ctx, moved, Target{}, nil)
	if err != nil {
		return false, err
	}
	a, err := json.Marshal(now.Rendered)
	if err != nil {
		return false, err
	}
	b, err := json.Marshal(then.Rendered)
	return !bytes.Equal(a, b), err
}
