package health_test

import (
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/tikhonp/proxier/internal/modules/servers/health"
	"github.com/tikhonp/proxier/internal/modules/servers/store"
	"github.com/tikhonp/proxier/internal/platform/jobs"
)

const prevIP = "198.51.100.7"

// keepPrevious gives the fixture's server a previous IP, as an IP change
// that kept the old one does.
func (f *fixture) keepPrevious(ip string) {
	f.T.Helper()
	if err := f.App.DB.Write(bg, func(tx *sqlx.Tx) error { return store.AddPreviousIP(bg, tx, f.ID, ip, f.at()) }); err != nil {
		f.T.Fatal(err)
	}
}

// testPrevious runs one previous IP test of the server.
func (f *fixture) testPrevious() {
	f.T.Helper()
	if _, err := f.App.Jobs.EnqueueNow(bg, jobs.Request{Type: health.JobPreviousTest, CreatedBy: "test",
		Payload: map[string]any{"server_id": f.ID}}); err != nil {
		f.T.Fatal(err)
	}
	f.Drain()
	f.Tick(time.Minute)
}

func (f *fixture) prev() store.PreviousIP {
	f.T.Helper()
	p, err := store.PreviousIPs(bg, f.App.DB.R, f.ID)
	if err != nil || len(p) != 1 {
		f.T.Fatalf("previous IPs: %+v %v", p, err)
	}
	return p[0]
}

func TestBlockedPreviousIPFailsAtOnceQuietly(t *testing.T) {
	f := newFixture(t)
	f.keepPrevious(prevIP)
	f.BlockIP(prevIP, true)
	f.testPrevious()
	if p := f.prev(); p.State != store.PreviousFails {
		t.Fatalf("state %s", p.State)
	}
	ev := f.Events("server.previous_ip_changed")
	if len(ev) != 1 || ev[0].Payload["from"] != "unknown" || ev[0].Payload["to"] != "fails" || ev[0].Payload["error"] == "" {
		t.Fatalf("events: %+v", ev)
	}
	if n := f.Notifying("server.previous_ip_changed"); n != 0 {
		t.Fatalf("%d notifications for an IP that was blocked when it was left", n)
	}
	r, ok, err := store.LatestPreviousResult(bg, f.App.DB.R, f.prev().ID)
	if err != nil || !ok || r.OK || r.Class != "stalled" || health.ParsePreviousDetail(r.Detail).FirstError() == "" {
		t.Fatalf("result %+v %v", r, err)
	}
}

func TestPreviousIPThatWorksAgainNotifiesAfterConfirmation(t *testing.T) {
	f := newFixture(t)
	f.keepPrevious(prevIP)
	f.BlockIP(prevIP, true)
	f.testPrevious()

	f.BlockIP(prevIP, false)
	f.testPrevious()
	if p := f.prev(); p.State != store.PreviousFails || p.CandidateCount != 1 {
		t.Fatalf("one passing test changed it: %+v", p)
	}
	f.testPrevious()
	if p := f.prev(); p.State != store.PreviousWorks {
		t.Fatalf("state %s", p.State)
	}
	if n := f.Notifying("server.previous_ip_changed"); n != 1 {
		t.Fatalf("%d notifications, want one: works again", n)
	}
	// A single failure after that is flap, not a change.
	f.BlockIP(prevIP, true)
	f.testPrevious()
	f.BlockIP(prevIP, false)
	f.testPrevious()
	if p := f.prev(); p.State != store.PreviousWorks || p.CandidateCount != 0 {
		t.Fatalf("after one failure: %+v", p)
	}
	if n := len(f.Events("server.previous_ip_changed")); n != 2 {
		t.Fatalf("%d changes", n)
	}
}

func TestPreviousIPResultIsInconclusiveWhileHomeIsOffline(t *testing.T) {
	f := newFixture(t)
	f.keepPrevious(prevIP)
	f.BlockIP(prevIP, true)
	f.SetRef("/dom", false)
	f.runJob(health.JobReference)
	f.testPrevious()
	if p := f.prev(); p.State != store.PreviousUnknown {
		t.Fatalf("state %s while home was offline", p.State)
	}
	r, ok, _ := store.LatestPreviousResult(bg, f.App.DB.R, f.prev().ID)
	if !ok || !r.Inconclusive {
		t.Fatalf("result %+v", r)
	}
}

func TestPausedServerKeepsItsPreviousIPsUntested(t *testing.T) {
	f := newFixture(t)
	f.keepPrevious(prevIP)
	if err := f.Svc.Pause(bg, []int64{f.ID}, 0, true, "member:1"); err != nil {
		t.Fatal(err)
	}
	f.testPrevious()
	if _, ok, _ := store.LatestPreviousResult(bg, f.App.DB.R, f.prev().ID); ok {
		t.Fatal("a paused server's previous IP was tested")
	}
	if ids, _ := store.ServersWithPreviousIPs(bg, f.App.DB.R); len(ids) != 0 {
		t.Fatalf("the round covers a paused server: %v", ids)
	}
}

func TestResultsFromBeforeAnIPChangeDoNotCount(t *testing.T) {
	f := newFixture(t)
	f.Healthy()
	f.Tick(time.Second)
	if err := f.App.DB.Write(bg, func(tx *sqlx.Tx) error {
		if err := store.SetServerIP(bg, tx, f.ID, "10.77.0.5", f.at()); err != nil {
			return err
		}
		return f.Svc.ResetForIPChange(bg, tx, f.ID, "job:9")
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.Health(); got.Health != health.Unknown {
		t.Fatalf("health %s", got.Health)
	}
	f.Tick(time.Second)
	f.Eval() // only the results from before the change exist
	if got := f.Health(); got.Health != health.Unknown {
		t.Fatalf("old results gave %s", got.Health)
	}
	f.Self(health.SelfOK)
	f.ProxyOK("main")
	f.Eval()
	if got := f.Health(); got.Health != health.Healthy {
		t.Fatalf("health %s after the new IP's checks", got.Health)
	}
}
