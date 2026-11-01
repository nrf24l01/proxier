package checkhost_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tikhonp/proxier/internal/modules/servers/checkhost"
	"github.com/tikhonp/proxier/internal/modules/servers/checkhost/checkhosttest"
)

var bg = context.Background()

func TestCheckTCPPolls(t *testing.T) {
	f := checkhosttest.New(t)
	c := f.Client()
	nodes, err := c.Nodes(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 8 || nodes[0].Name != "de1.node.check-host.net" || nodes[0].Country != "de" || nodes[0].City != "Nuremberg" {
		t.Fatalf("nodes %+v", nodes)
	}

	// The nodes answer null for the first three polls; the client keeps asking.
	f.PollsBeforeAnswer(3)
	c.Wait = 2 * time.Second
	res, err := c.CheckTCP(bg, "203.0.113.5:443", []string{"ru1.node.check-host.net", "de1.node.check-host.net"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || !res[0].OK || res[0].ConnectMS != 20 || !res[1].OK {
		t.Fatalf("%+v", res)
	}
	if got := f.Checks(); len(got) != 1 || got[0].HostPort != "203.0.113.5:443" || len(got[0].Nodes) != 2 {
		t.Fatalf("checks %+v", got)
	}

	// A refusing service is "unavailable", not an empty result.
	f.Fail(503)
	if _, err := c.CheckTCP(bg, "203.0.113.5:443", []string{"ru1.node.check-host.net"}); !errors.Is(err, checkhost.ErrUnavailable) {
		t.Fatalf("err %v", err)
	}
	if _, err := c.Nodes(bg); !errors.Is(err, checkhost.ErrUnavailable) {
		t.Fatalf("err %v", err)
	}
}

func TestNodeAnswers(t *testing.T) {
	f := checkhosttest.New(t)
	c := f.Client()
	host := "203.0.113.5:443"
	f.Set(host, "ru1.node.check-host.net", checkhosttest.Answer{MS: 45})
	f.Set(host, "ru2.node.check-host.net", checkhosttest.Answer{Error: "Connection timed out"})
	f.Set(host, "ru3.node.check-host.net", checkhosttest.Answer{Error: "Connection refused"})
	f.Set(host, "de1.node.check-host.net", checkhosttest.Answer{Pending: true})
	res, err := c.CheckTCP(bg, host, []string{"ru1.node.check-host.net", "ru2.node.check-host.net", "ru3.node.check-host.net", "de1.node.check-host.net"})
	if err != nil {
		t.Fatal(err)
	}
	want := []checkhost.NodeResult{
		{Node: "ru1.node.check-host.net", OK: true, ConnectMS: 45},
		{Node: "ru2.node.check-host.net", Error: "Connection timed out"},
		{Node: "ru3.node.check-host.net", Error: "Connection refused"},
		{Node: "de1.node.check-host.net", Error: "no answer"},
	}
	for i := range want {
		if res[i] != want[i] {
			t.Errorf("node %d: got %+v want %+v", i, res[i], want[i])
		}
	}
}

// route reaches the fake whatever address is asked, and counts its dials.
func route(f *checkhosttest.Fake, dials *int) checkhost.Route {
	d := &net.Dialer{}
	tr := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		*dials++
		return d.DialContext(ctx, network, f.Addr())
	}}
	return checkhost.Route{Name: "nl-1", HTTP: &http.Client{Transport: tr}}
}

// TestBlockedServiceGoesAround: when the direct way gets no answer, the client
// opens its route once and asks everything else of the check through it.
func TestBlockedServiceGoesAround(t *testing.T) {
	f := checkhosttest.New(t)
	f.Block(true)
	opened, dials := 0, 0
	c := f.Client()
	c.Via = func(context.Context) (checkhost.Route, error) { opened++; return route(f, &dials), nil }
	f.PollsBeforeAnswer(2)
	c.Wait = 2 * time.Second
	res, err := c.CheckTCP(bg, "203.0.113.5:443", []string{"ru1.node.check-host.net"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK || opened != 1 || c.Through() != "nl-1" || dials == 0 {
		t.Fatalf("res %+v opened %d through %q dials %d", res, opened, c.Through(), dials)
	}
	if _, err := c.Nodes(bg); err != nil || opened != 1 {
		t.Fatalf("nodes: %v, opened %d", err, opened)
	}
}

// TestHTTPErrorDoesNotGoAround: an answer, even a refusal, means the direct
// way works, so the route is not opened.
func TestHTTPErrorDoesNotGoAround(t *testing.T) {
	f := checkhosttest.New(t)
	f.Fail(503)
	c := f.Client()
	c.Via = func(context.Context) (checkhost.Route, error) {
		t.Fatal("opened a route")
		return checkhost.Route{}, nil
	}
	if _, err := c.Nodes(bg); !errors.Is(err, checkhost.ErrUnavailable) || c.Through() != "" {
		t.Fatalf("err %v through %q", err, c.Through())
	}
}

// TestNoWayAround: without a route the error says both what failed and why
// there is no way around it, and the client does not try again.
func TestNoWayAround(t *testing.T) {
	f := checkhosttest.New(t)
	f.Block(true)
	opened := 0
	c := f.Client()
	c.Via = func(context.Context) (checkhost.Route, error) {
		opened++
		return checkhost.Route{}, errors.New("no healthy server")
	}
	_, err := c.Nodes(bg)
	if !errors.Is(err, checkhost.ErrUnavailable) || !strings.Contains(err.Error(), "TLS handshake timeout") || !strings.Contains(err.Error(), "no way around it: no healthy server") {
		t.Fatalf("err %v", err)
	}
	if _, err := c.Nodes(bg); err == nil || opened != 1 {
		t.Fatalf("err %v opened %d", err, opened)
	}
}
