package store

import (
	"context"
	"database/sql"
	"time"
)

// Aggregation lives in Postgres, not Go. The naive version pulled every
// raw row across and reduced it here, so one page load moved the whole
// history per service. These queries return one row per bucket or per
// service instead, and the bytes on the wire stay flat as history grows.

// ServiceWindow names one service's aggregation: how wide a bucket is and how
// far back to look.
type ServiceWindow struct {
	ID            string
	BucketSeconds int64
	Since         time.Time
}

func splitWindows(windows []ServiceWindow) (ids []string, buckets []int64, since []time.Time) {
	ids = make([]string, len(windows))
	buckets = make([]int64, len(windows))
	since = make([]time.Time, len(windows))
	for i, w := range windows {
		ids[i] = w.ID
		buckets[i] = w.BucketSeconds
		since[i] = w.Since
	}
	return ids, buckets, since
}

// LatestPerAgent returns each agent's most recent check per service, which is
// all Aggregate needs to decide the current status.
func (s *Store) LatestPerAgent(ctx context.Context, since time.Time) (map[string][]CheckRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT ON (service_id, agent_id)
		       service_id, agent_id, checked_at, success, COALESCE(latency_ms, 0)
		FROM checks
		WHERE checked_at >= $1
		ORDER BY service_id, agent_id, checked_at DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]CheckRow{}
	for rows.Next() {
		var id string
		var r CheckRow
		if err := rows.Scan(&id, &r.AgentID, &r.CheckedAt, &r.Success, &r.LatencyMS); err != nil {
			return nil, err
		}
		out[id] = append(out[id], r)
	}
	return out, rows.Err()
}

// BucketedHistory returns per-bucket status for many services in one round
// trip, oldest first. Empty buckets come back omitted. The caller renders
// those gaps as unknown.
func (s *Store) BucketedHistory(ctx context.Context, windows []ServiceWindow) (map[string][]HistoryPoint, error) {
	if len(windows) == 0 {
		return map[string][]HistoryPoint{}, nil
	}
	ids, buckets, since := splitWindows(windows)

	rows, err := s.db.QueryContext(ctx, `
		SELECT w.service_id,
		       floor(extract(epoch FROM c.checked_at) / w.bucket)::bigint AS bucket,
		       bool_or(c.success) AS up,
		       bool_or(NOT c.success) AS has_failure
		FROM unnest($1::text[], $2::bigint[], $3::timestamptz[]) AS w(service_id, bucket, since)
		JOIN checks c ON c.service_id = w.service_id AND c.checked_at >= w.since
		GROUP BY 1, 2
		ORDER BY 1, 2`, ids, buckets, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	size := map[string]int64{}
	for _, w := range windows {
		size[w.ID] = w.BucketSeconds
	}

	out := map[string][]HistoryPoint{}
	for rows.Next() {
		var id string
		var bucket int64
		var up, hasFailure bool
		if err := rows.Scan(&id, &bucket, &up, &hasFailure); err != nil {
			return nil, err
		}
		out[id] = append(out[id], HistoryPoint{
			Time:   time.Unix(bucket*size[id], 0).UTC(),
			Status: BucketStatus(up, hasFailure),
		})
	}
	return out, rows.Err()
}

// UptimeRatios returns the share of buckets that were up, per service, as a
// percentage. Services with no data in the window are absent from the map.
func (s *Store) UptimeRatios(ctx context.Context, windows []ServiceWindow) (map[string]float64, error) {
	if len(windows) == 0 {
		return map[string]float64{}, nil
	}
	ids, buckets, since := splitWindows(windows)

	rows, err := s.db.QueryContext(ctx, `
		WITH bucketed AS (
			SELECT w.service_id AS service_id,
			       floor(extract(epoch FROM c.checked_at) / w.bucket)::bigint AS bucket,
			       bool_or(c.success) AS up
			FROM unnest($1::text[], $2::bigint[], $3::timestamptz[]) AS w(service_id, bucket, since)
			JOIN checks c ON c.service_id = w.service_id AND c.checked_at >= w.since
			GROUP BY 1, 2
		)
		SELECT service_id,
		       count(*) FILTER (WHERE up) * 100.0 / count(*)
		FROM bucketed
		GROUP BY 1`, ids, buckets, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]float64{}
	for rows.Next() {
		var id string
		var pct float64
		if err := rows.Scan(&id, &pct); err != nil {
			return nil, err
		}
		out[id] = pct
	}
	return out, rows.Err()
}

// Trend returns up to limit buckets of average latency for one service,
// oldest first.
func (s *Store) Trend(ctx context.Context, serviceID string, since time.Time, bucketSeconds int64, limit int) ([]TrendPoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT bucket, up, has_failure, avg_ms FROM (
			SELECT floor(extract(epoch FROM checked_at) / $3)::bigint AS bucket,
			       bool_or(success) AS up,
			       bool_or(NOT success) AS has_failure,
			       avg(latency_ms) FILTER (WHERE success) AS avg_ms
			FROM checks
			WHERE service_id = $1 AND checked_at >= $2
			GROUP BY 1
			ORDER BY 1 DESC
			LIMIT $4
		) t ORDER BY bucket`, serviceID, since, bucketSeconds, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TrendPoint
	for rows.Next() {
		var bucket int64
		var up, hasFailure bool
		var avg sql.NullFloat64
		if err := rows.Scan(&bucket, &up, &hasFailure, &avg); err != nil {
			return nil, err
		}
		p := TrendPoint{
			Time:   time.Unix(bucket*bucketSeconds, 0).UTC(),
			AvgMS:  -1,
			Status: BucketStatus(up, hasFailure),
		}
		if avg.Valid {
			p.AvgMS = int(avg.Float64 + 0.5)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LatencyStatsSince summarises successful checks in one window.
func (s *Store) LatencyStatsSince(ctx context.Context, serviceID string, since time.Time) (LatencyStats, error) {
	var st LatencyStats
	var avg, min, max sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT avg(latency_ms), min(latency_ms), max(latency_ms), count(*)
		FROM checks
		WHERE service_id = $1 AND checked_at >= $2 AND success`, serviceID, since).
		Scan(&avg, &min, &max, &st.Samples)
	if err != nil {
		return st, err
	}
	if avg.Valid {
		st.AvgMS = int(avg.Float64 + 0.5)
		st.MinMS = int(min.Float64)
		st.MaxMS = int(max.Float64)
	}
	return st, nil
}

// RecentChecks returns the newest raw check rows for one service.
func (s *Store) RecentChecks(ctx context.Context, serviceID string, limit int) ([]CheckRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, checked_at, success, COALESCE(latency_ms, 0), COALESCE(error, '')
		FROM checks
		WHERE service_id = $1
		ORDER BY checked_at DESC
		LIMIT $2`, serviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CheckRow
	for rows.Next() {
		var r CheckRow
		if err := rows.Scan(&r.AgentID, &r.CheckedAt, &r.Success, &r.LatencyMS, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StatusChanges returns the moments a service flipped between up and down,
// newest first. The comparison runs in Postgres so only the transitions come
// back, not every bucket in the window.
func (s *Store) StatusChanges(ctx context.Context, serviceID string, since time.Time, bucketSeconds int64, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH bucketed AS (
			SELECT floor(extract(epoch FROM checked_at) / $3)::bigint AS bucket,
			       bool_or(success) AS up
			FROM checks
			WHERE service_id = $1 AND checked_at >= $2
			GROUP BY 1
		), flagged AS (
			SELECT bucket, up, lag(up) OVER (ORDER BY bucket) AS prev FROM bucketed
		)
		SELECT bucket, up
		FROM flagged
		WHERE prev IS NULL OR up <> prev
		ORDER BY bucket DESC
		LIMIT $4`, serviceID, since, bucketSeconds, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var bucket int64
		var up bool
		if err := rows.Scan(&bucket, &up); err != nil {
			return nil, err
		}
		status := StatusDown
		if up {
			status = StatusUp
		}
		out = append(out, Event{Time: time.Unix(bucket*bucketSeconds, 0).UTC(), Status: status})
	}
	return out, rows.Err()
}
