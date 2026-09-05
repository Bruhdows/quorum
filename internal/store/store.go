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
`

// Open connects to Postgres and ensures the schema exists.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
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

func (s *Store) InsertResult(ctx context.Context, serviceID, agentID string, checkedAt time.Time, success bool, latencyMS int, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO checks (service_id, agent_id, checked_at, success, latency_ms, error) VALUES ($1, $2, $3, $4, $5, $6)`,
		serviceID, agentID, checkedAt, success, latencyMS, errMsg,
	)
	return err
}

// RecentRows returns all check rows for a service within the given window.
// The order is unspecified; callers sort or bucket as needed.
func (s *Store) RecentRows(ctx context.Context, serviceID string, since time.Time) ([]CheckRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT agent_id, checked_at, success, COALESCE(latency_ms, 0) FROM checks WHERE service_id = $1 AND checked_at >= $2`,
		serviceID, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CheckRow
	for rows.Next() {
		var r CheckRow
		if err := rows.Scan(&r.AgentID, &r.CheckedAt, &r.Success, &r.LatencyMS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneOlderThan deletes check rows older than the cutoff. It's a plain
// DELETE on an unpartitioned table, which is fine at this scale; if the
// table grows large enough that the delete itself becomes slow, partition
// by month instead.
func (s *Store) PruneOlderThan(ctx context.Context, cutoff time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM checks WHERE checked_at < $1`, cutoff)
	return err
}
