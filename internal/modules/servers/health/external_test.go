package health_test

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/tikhonp/proxier/internal/modules/servers/checkhost/checkhosttest"
	"github.com/tikhonp/proxier/internal/modules/servers/endpoint"
	"github.com/tikhonp/proxier/internal/modules/servers/health"
	"github.com/tikhonp/proxier/internal/modules/servers/proxy"
	"github.com/tikhonp/proxier/internal/modules/servers/store"
	"github.com/tikhonp/proxier/internal/platform/jobs"
)

func (f *fixture) setNodes() {
	f.T.Helper()
	if err := f.App.Settings.Set(bg, "admin", "servers", map[string]string{
		"servers.external_nodes_ru":     "ru1.node.check-host.net,ru2.node.check-host.net,ru3.node.check-host.net",
		"servers.external_nodes_abroad": "de1.node.check-host.net,nl1.node.check-host.net,se1.node.check-host.net",
	}); err != nil {
		f.T.Fatal(err)
	}
}

func (f *fixture) queueExternal(kind string) {
	f.T.Helper()
	if _, err := f.App.Jobs.EnqueueNow(bg, jobs.Request{Type: health.JobExternal, Subject: store.ServerSubject(f.ID), CreatedBy: "test",
		Payload: map[string]any{"server_id": f.ID, "kind": kind}}); err != nil {
		f.T.Fatal(err)
	}
}

func (f *fixture) externalRows(class string) int {
	f.T.Helper()
	var n int
	if err := f.App.DB.R.Get(&n, `SELECT count(*) FROM servers_check_results WHERE kind = 'external' AND class = ?`, class); err != nil {
		f.T.Fatal(err)
	}
	return n
}

// TestOnDemandExternalIsAwaited: a failing proxy test with an external result
// 25 minutes old asks for a new one, and the verdict waits for it.
func TestOnDemandExternalIsAwaited(t *testing.T) {
	f := newFixture(t)
	f.setNodes()
	f.Healthy()
	host := f.Server(f.ID).IP + ":443"
	f.CH.Set(host, "de1.node.check-host.net", checkhosttest.Answer{MS: 30})
	f.CH.Set(host, "nl1.node.check-host.net", checkhosttest.Answer{MS: 30})
	f.CH.Set(host, "ru1.node.check-host.net", checkhosttest.Answer{Error: "Connection timed out"})
	f.CH.Set(host, "ru2.node.check-host.net", checkhosttest.Answer{Error: "Connection timed out"})
	f.CH.Set(host, "ru3.node.check-host.net", checkhosttest.Answer{Error: "Connection timed out"})

	f.Tick(25 * time.Minute)
	f.External([]bool{true, true, true}, []bool{true, true, true}) // 25 min old by the time it is needed
	f.Tick(25 * time.Minute)
	f.Self(health.SelfOK)

	// A failing proxy round: the job stores it, asks for an external check and
	// the evaluation waits.
	f.VPS.SetService("nginx", "running")
	f.StallSmoke(true)
	if err := f.queueProxytest(); err != nil {
		t.Fatal(err)
	}
	f.WaitFor("the proxy test", func() bool { return f.countResults("proxy") == 2 })
	f.Drain()

	// The external check ran, and its own evaluation made the first candidate.
	if len(f.CH.Checks()) != 1 {
		t.Fatalf("%d check-host requests", len(f.CH.Checks()))
	}
	if h := f.Health(); health.ParseDetail(h.Detail).Awaiting != "" {
		t.Errorf("still awaiting: %s", h.Detail)
	}
	if h := f.Health(); h.Candidate.String != health.Blocked || h.CandidateCount != 1 {
		t.Fatalf("candidate %v count %d: the external check's evaluation is the counted one", h.Candidate, h.CandidateCount)
	}
	var queued int
	if err := f.App.DB.R.Get(&queued, `SELECT count(*) FROM jobs WHERE type = ? AND state = 'queued'`, health.JobEvaluate); err != nil || queued != 0 {
		t.Errorf("%d delayed evaluations still queued after the external check answered (%v)", queued, err)
	}
}

// TestOnDemandWaitsWhenNothingAnswers: the evaluation stores nothing while the
// external check is on its way, and looks again after EvalDelay.
func TestOnDemandWaitsWhenNothingAnswers(t *testing.T) {
	f := newFixture(t)
	f.Healthy()
	f.Tick(5 * time.Minute)
	f.Self(health.SelfOK)
	f.ProxyFail("main", "stalled", "x")
	// the proxy test job marked the wait
	if err := f.App.DB.Write(bg, func(tx *sqlx.Tx) error { return f.Svc.MarkAwaiting(bg, tx, f.ID, f.Clock.Now()) }); err != nil {
		t.Fatal(err)
	}
	before := f.Health()
	f.Eval()
	after := f.Health()
	if after.CandidateCount != before.CandidateCount || after.Candidate != before.Candidate {
		t.Fatalf("a waiting evaluation stored a candidate: %+v", after)
	}
	if f.countJobs(health.JobEvaluate) != 1 {
		t.Fatalf("no delayed evaluation was queued")
	}
	// after two minutes without an answer the evaluation decides without it
	f.Tick(3 * time.Minute)
	f.Self(health.SelfOK)
	f.ProxyFail("main", "stalled", "x")
	f.Eval()
	if h := f.Health(); h.Candidate.String != health.Blocked {
		t.Fatalf("candidate %v", h.Candidate)
	}
}

func TestCheckHostErrorIsUnconfirmed(t *testing.T) {
	f := newFixture(t)
	f.setNodes()
	f.Healthy()
	f.CH.Fail(503)
	for i := 0; i < 2; i++ {
		f.Tick(5 * time.Minute)
		f.Self(health.SelfOK)
		f.ProxyFail("main", "stalled", "x")
		f.queueExternal(health.ExternalOnDemand)
		f.Drain()
	}
	if n := f.externalRows("unavailable"); n == 0 {
		t.Fatal("no unavailable row")
	}
	// the evaluation after the failed check has no external data
	f.Eval()
	if h := f.Health(); h.Health != health.Blocked {
		t.Fatalf("health %s", h.Health)
	}
	if !strings.Contains(f.ReasonText(), "unconfirmed from abroad") {
		t.Errorf("reason %q", f.ReasonText())
	}
}

func TestExternalBudget(t *testing.T) {
	f := newFixture(t)
	f.setNodes()
	f.Healthy()

	// a first on-demand check runs
	f.queueExternal(health.ExternalOnDemand)
	f.Drain()
	if n := f.externalRows("skipped"); n != 0 || len(f.CH.Checks()) != 1 {
		t.Fatalf("first: %d skipped, %d requests", n, len(f.CH.Checks()))
	}
	// a second one within 10 minutes is skipped and recorded
	f.Tick(5 * time.Minute)
	f.queueExternal(health.ExternalOnDemand)
	f.Drain()
	if n := f.externalRows("skipped"); n != 1 || len(f.CH.Checks()) != 1 {
		t.Fatalf("second: %d skipped, %d requests", n, len(f.CH.Checks()))
	}
	// a scheduled check 29 min 50 s after the first result still runs: the
	// per-server limit is 80 % of the interval
	f.Tick(24*time.Minute + 50*time.Second)
	f.queueExternal(health.ExternalScheduled)
	f.Drain()
	if len(f.CH.Checks()) != 2 {
		t.Fatalf("the scheduled check did not run: %d requests", len(f.CH.Checks()))
	}
	// the hourly cap: with it reached, a due check is skipped
	if err := f.App.Settings.Set(bg, "admin", "servers", map[string]string{"servers.external_hourly_cap": "2"}); err != nil {
		t.Fatal(err)
	}
	f.Tick(30 * time.Minute)
	f.queueExternal(health.ExternalScheduled)
	f.Drain()
	if len(f.CH.Checks()) != 2 || f.externalRows("skipped") != 2 {
		t.Fatalf("over the cap: %d requests, %d skipped", len(f.CH.Checks()), f.externalRows("skipped"))
	}
	// skipped rows are not data: the verdict has no external result
	st, err := f.Svc.Load(bg, f.ID, f.Clock.Now())
	if err != nil || st.External != nil {
		t.Fatalf("external %v %v", st.External, err)
	}
}

// dialFake makes routes around the block reach the fake check-host and
// records which endpoint each went through.
func (f *fixture) dialFake() *[]string {
	var through []string
	var mu sync.Mutex
	f.Svc.Dial = func(_ context.Context, e endpoint.Endpoint) (proxy.DialFunc, io.Closer, error) {
		mu.Lock()
		through = append(through, e.Host)
		mu.Unlock()
		d := &net.Dialer{}
		return func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, f.CH.Addr())
		}, io.NopCloser(nil), nil
	}
	return &through
}

// TestBlockedCheckHostGoesThroughAServer: check-host's addresses get no
// answer from home, so the external check asks it through a healthy server,
// another than the one it checks, and stores the nodes' answers as usual.
func TestBlockedCheckHostGoesThroughAServer(t *testing.T) {
	f := newFixture(t)
	f.setNodes()
	f.Healthy()
	other := f.AddServer("10.77.0.2")
	if _, err := f.App.DB.W.Exec(`UPDATE servers_servers SET health = 'healthy' WHERE id = ?`, other); err != nil {
		t.Fatal(err)
	}
	f.CH.Block(true)
	through := f.dialFake()

	f.queueExternal(health.ExternalScheduled)
	f.Drain()

	if len(f.CH.Checks()) != 1 || f.externalRows("unavailable") != 0 {
		t.Fatalf("%d checks, %d unavailable", len(f.CH.Checks()), f.externalRows("unavailable"))
	}
	var ok int
	if err := f.App.DB.R.Get(&ok, `SELECT count(*) FROM servers_check_results WHERE kind = 'external' AND server_id = ? AND ok = 1`, f.ID); err != nil {
		t.Fatal(err)
	}
	if ok != 6 {
		t.Fatalf("%d nodes connected, want 6", ok)
	}
	if want := f.Server(other).ProxyHostname; len(*through) != 1 || (*through)[0] != want {
		t.Fatalf("went through %v, want %s", *through, want)
	}
	if log := f.Log(f.LastJob(health.JobExternal)); !strings.Contains(log, "asked through "+f.Server(other).Name) {
		t.Fatalf("log:\n%s", log)
	}
}

// TestBlockedCheckHostWithoutAHealthyServer: with no healthy server to go
// through, the check is unavailable and says why.
func TestBlockedCheckHostWithoutAHealthyServer(t *testing.T) {
	f := newFixture(t)
	f.setNodes()
	f.CH.Block(true)
	through := f.dialFake()

	f.queueExternal(health.ExternalScheduled)
	f.Drain()

	var detail string
	if err := f.App.DB.R.Get(&detail, `SELECT detail FROM servers_check_results WHERE kind = 'external' AND class = 'unavailable'`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "no server is healthy to go through") || len(*through) != 0 || len(f.CH.Checks()) != 0 {
		t.Fatalf("detail %s, through %v, %d checks", detail, *through, len(f.CH.Checks()))
	}
}
