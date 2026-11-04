// Package agent hands a Shadowrocket config's base config to a coding agent
// (docs/processes/routing/shadowrocket-config.md, "handing the base config
// to an agent"). Proxier never runs the agent: it gives the member a prompt
// with a short-lived token that reaches one config's base through
// /agent/v1/shadowrocket and nothing else. Hidden values never leave
// Proxier (mask.go).
package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/tikhonp/proxier/internal/modules/routing/shadowrocket"
	"github.com/tikhonp/proxier/internal/modules/routing/store"
	"github.com/tikhonp/proxier/internal/platform/access"
	"github.com/tikhonp/proxier/internal/platform/agentapi"
	"github.com/tikhonp/proxier/internal/platform/db"
	"github.com/tikhonp/proxier/internal/platform/events"
	"github.com/tikhonp/proxier/internal/platform/jobs"
	"github.com/tikhonp/proxier/internal/platform/vault"
)

var (
	ErrNotFound  = store.ErrNotFound
	ErrNotOpen   = errors.New("the session is not open")
	ErrNoProblem = errors.New("say what the agent should do")
	ErrBadAgent  = errors.New("unknown agent")
)

// Deps is what the service is built from.
type Deps struct {
	DB           *db.DB
	Events       *events.Catalog
	Vault        *vault.Vault
	Shadowrocket *shadowrocket.Service
	Log          *slog.Logger
	// BaseURL is Proxier's own address, as the prompt shows it.
	BaseURL string
	Now     func() time.Time
}

// Service keeps the sessions.
type Service struct{ Deps }

// New returns the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{Deps: d}
}

func (s *Service) now() db.Time { return db.At(s.Now()) }

// Open is what the member chose in the hand-off form.
type Open struct {
	ConfigID int64
	Problem  string
	Agent    string // claude-code codex other
	For      time.Duration
}

// Opened is the answer to opening a session. The token is shown once.
type Opened struct {
	SessionID int64
	Token     string
	Prompt    string
	ExpiresAt time.Time
}

// OpenSession hands the config's base to an agent. The caller has checked
// that the member may change the config. It closes the config's open
// session (reason replaced) in the same transaction, and returns the token
// and the prompt that holds it; neither is stored in a readable form.
func (s *Service) OpenSession(ctx context.Context, o Open, actor string) (Opened, error) {
	var ok bool
	if o.Problem, ok = agentapi.Problem(o.Problem); !ok {
		return Opened{}, ErrNoProblem
	}
	if !agentapi.KnownAgent(o.Agent) {
		return Opened{}, ErrBadAgent
	}
	o.For = agentapi.Clamp(o.For)
	cfg, err := s.Shadowrocket.Get(ctx, o.ConfigID, access.All)
	if errors.Is(err, shadowrocket.ErrNotFound) {
		return Opened{}, ErrNotFound
	}
	if err != nil {
		return Opened{}, err
	}

	token := agentapi.NewToken()
	opened := s.now()
	expires := db.At(opened.Add(o.For))
	var id int64
	err = s.DB.Write(ctx, func(tx *sqlx.Tx) error {
		// The partial unique index allows one open session per config: end the
		// old one first. One whose time already passed ended by expiry, not by us.
		if old, err := store.OpenAgentSession(ctx, tx, o.ConfigID); err == nil {
			reason := "replaced"
			if !old.ExpiresAt.After(opened.Time) {
				reason = "expired"
			}
			if err := s.close(ctx, tx, old, reason, actor); err != nil {
				return err
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		id, err = store.InsertAgentSession(ctx, tx, store.AgentSession{
			ConfigID: o.ConfigID, TokenLookup: s.Vault.Lookup(token), Problem: o.Problem,
			Agent: o.Agent, OpenedAt: opened, ExpiresAt: expires,
		})
		if err != nil {
			return err
		}
		_, err = s.Events.Record(ctx, tx, events.Event{Time: opened, Type: "routing.shadowrocket_agent_session_opened", Subject: shadowrocket.Subject(o.ConfigID), Actor: actor,
			Payload: map[string]any{"expires_at": expires.Format(db.TimeLayout), "agent": o.Agent, "session": id}})
		return err
	})
	if err != nil {
		return Opened{}, err
	}
	return Opened{
		SessionID: id, Token: token, ExpiresAt: expires.Time,
		Prompt: Prompt(PromptData{Agent: o.Agent, Config: cfg.File(), Problem: o.Problem, BaseURL: s.BaseURL, Token: token, Expires: expires.Time}),
	}, nil
}

// close ends an open session in tx and records
// routing.shadowrocket_agent_session_closed.
func (s *Service) close(ctx context.Context, tx *sqlx.Tx, sess store.AgentSession, reason, actor string) error {
	ok, err := store.CloseAgentSession(ctx, tx, sess.ID, reason, s.now())
	if err != nil || !ok {
		return err
	}
	_, err = s.Events.Record(ctx, tx, events.Event{Time: s.now(), Type: "routing.shadowrocket_agent_session_closed", Subject: shadowrocket.Subject(sess.ConfigID), Actor: actor,
		Payload: map[string]any{"reason": reason, "saves": sess.Saves, "session": sess.ID}})
	return err
}

// Revoke ends a session of the config at the member's request: ErrNotFound
// when the config has no such session, ErrNotOpen when it already ended.
func (s *Service) Revoke(ctx context.Context, configID, sessionID int64, actor string) error {
	return s.DB.Write(ctx, func(tx *sqlx.Tx) error {
		sess, err := store.AgentSessionByID(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if sess.ConfigID != configID {
			return ErrNotFound
		}
		if !sess.Open() {
			return ErrNotOpen
		}
		return s.close(ctx, tx, sess, "revoked", actor)
	})
}

// Current returns the config's open session, ErrNotFound when none. A
// session whose time has passed but that the schedule has not closed yet
// counts as ended.
func (s *Service) Current(ctx context.Context, configID int64) (store.AgentSession, error) {
	sess, err := store.OpenAgentSession(ctx, s.DB.R, configID)
	if err != nil {
		return sess, err
	}
	if !sess.ExpiresAt.After(s.Now()) {
		return store.AgentSession{}, store.ErrNotFound
	}
	return sess, nil
}

// JobType is the job the schedule runs to close expired sessions.
const JobType = "routing.shadowrocket_agent_sessions"

// Expire closes every open session whose time has passed, recording
// routing.shadowrocket_agent_session_closed{expired}. It returns how many it
// closed.
func (s *Service) Expire(ctx context.Context, actor string) (int, error) {
	list, err := store.ExpiredAgentSessions(ctx, s.DB.R, s.now())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sess := range list {
		err := s.DB.Write(ctx, func(tx *sqlx.Tx) error {
			cur, err := store.AgentSessionByID(ctx, tx, sess.ID)
			if err != nil || !cur.Open() {
				return err
			}
			n++
			return s.close(ctx, tx, cur, "expired", actor)
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// JobTypes: the expiry job.
func (s *Service) JobTypes() []jobs.Type {
	return []jobs.Type{{
		Name: JobType, Queue: jobs.Maintenance, MaxAttempts: 1, Quiet: true,
		Steps: []jobs.Step{{Name: "expire", Run: func(ctx context.Context, r *jobs.Run) error {
			n, err := s.Expire(ctx, r.Info().Actor())
			if n > 0 {
				r.Log().Info("Closed %d expired agent sessions", n)
			}
			return err
		}}},
	}}
}

// Schedule closes expired sessions every five minutes. Expiry is enforced on
// every request as well; this is what records it.
func (s *Service) Schedule() jobs.Schedule {
	return jobs.Schedule{Name: JobType, Every: 5 * time.Minute,
		Request: func(context.Context) (jobs.Request, error) {
			return jobs.Request{Type: JobType, CoalescingKey: "shadowrocket_agent_sessions"}, nil
		}}
}
