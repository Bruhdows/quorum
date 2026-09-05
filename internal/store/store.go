package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS checks (
	id          BIGSERIAL PRIMARY KEY,
	service_id  TEXT NOT NULL,
	agent_id    TEXT NOT NULL,
	checked_at  TIMESTAMPTZ NOT NULL,
	success     BOOLEAN NOT NULL,
	latency_ms  INTEGER,
	error       TEXT
);
CREATE INDEX IF NOT EXISTS checks_service_time_idx ON checks (service_id, checked_at DESC);
-- Serves the "latest check per agent" lookup without sorting the whole window.
CREATE INDEX IF NOT EXISTS checks_service_agent_time_idx ON checks (service_id, agent_id, checked_at DESC);
-- Pruning scans by time across every service, which neither index above covers.
CREATE INDEX IF NOT EXISTS checks_time_idx ON checks (checked_at);
`

// Pruning deletes in chunks so one nightly DELETE never holds a lock over
// millions of rows at once.
const pruneBatch = 10000

// Open connects to Postgres and ensures the schema exists.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// A burst of agents posting results opens a connection each, and
	// without limits Postgres runs out of backends before the hub notices.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Result is one completed check, ready to store.
type Result struct {
	ServiceID string
	AgentID   string
	CheckedAt time.Time
	Success   bool
	LatencyMS int
	Error     string
}

func (s *Store) InsertResult(ctx context.Context, serviceID, agentID string, checkedAt time.Time, success bool, latencyMS int, errMsg string) error {
	return s.InsertResults(ctx, []Result{{
		ServiceID: serviceID,
		AgentID:   agentID,
		CheckedAt: checkedAt,
		Success:   success,
		LatencyMS: latencyMS,
		Error:     errMsg,
	}})
}

// InsertResults writes a whole batch in one statement. An agent watching many
// services produces a steady stream of single checks; sending them together
// turns a round trip per check into a round trip per flush.
func (s *Store) InsertResults(ctx context.Context, results []Result) error {
	if len(results) == 0 {
		return nil
	}
	ids := make([]string, len(results))
	agents := make([]string, len(results))
	times := make([]time.Time, len(results))
	oks := make([]bool, len(results))
	latencies := make([]int32, len(results))
	errs := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ServiceID
		agents[i] = r.AgentID
		times[i] = r.CheckedAt
		oks[i] = r.Success
		latencies[i] = int32(r.LatencyMS)
		errs[i] = r.Error
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO checks (service_id, agent_id, checked_at, success, latency_ms, error)
		SELECT * FROM unnest($1::text[], $2::text[], $3::timestamptz[], $4::bool[], $5::int[], $6::text[])`,
		ids, agents, times, oks, latencies, errs)
	return err
}

// PruneOlderThan deletes check rows older than the cutoff, in batches. One
// unbounded DELETE over months of rows would hold locks and bloat WAL the
// whole time it runs. A loop of small deletes lets other work through
// between them.
func (s *Store) PruneOlderThan(ctx context.Context, cutoff time.Time) error {
	for {
		res, err := s.db.ExecContext(ctx, `
			DELETE FROM checks
			WHERE id IN (SELECT id FROM checks WHERE checked_at < $1 LIMIT $2)`, cutoff, pruneBatch)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n < pruneBatch {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}
