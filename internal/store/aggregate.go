package store

import "time"

type CheckRow struct {
	AgentID   string
	CheckedAt time.Time
	Success   bool
	LatencyMS int
	Error     string
}

// Status is the aggregated state of a service across all agents.
type Status string

const (
	StatusUp       Status = "up"
	StatusDown     Status = "down"
	StatusUnknown  Status = "unknown"
	StatusDegraded Status = "degraded"
)

// BucketStatus maps one bucket's outcomes to a display status. All
// successes read up, all failures down, a mix degraded. Empty buckets never
// reach here. The caller renders those as unknown.
func BucketStatus(up, hasFailure bool) Status {
	switch {
	case up && !hasFailure:
		return StatusUp
	case up:
		return StatusDegraded
	default:
		return StatusDown
	}
}

// The shapes the aggregation queries in query.go return. Bucketing and
// averaging happen in Postgres, so nothing here walks raw rows except
// Aggregate, which only ever sees one row per agent.

type HistoryPoint struct {
	Time   time.Time
	Status Status
}

type TrendPoint struct {
	Time   time.Time
	AvgMS  int // -1 when the bucket holds no successful check
	Status Status
}

type LatencyStats struct {
	AvgMS   int
	MinMS   int
	MaxMS   int
	Samples int
}

type Event struct {
	Time   time.Time
	Status Status
}

// Aggregate folds each agent's latest check into one status. Up needs a
// single recent success. Down means every recent reporter failed. Unknown
// means nothing recent came in at all. It also returns the best latency
// among the succeeding agents and the newest check time overall.
func Aggregate(rows []CheckRow, staleness time.Duration, now time.Time) (status Status, latencyMS *int, lastChecked *time.Time) {
	latest := map[string]CheckRow{}
	for _, r := range rows {
		cur, ok := latest[r.AgentID]
		if !ok || r.CheckedAt.After(cur.CheckedAt) {
			latest[r.AgentID] = r
		}
	}

	var anySuccess, anyRecent bool
	var bestLatency *int
	var last *time.Time
	for _, r := range latest {
		if last == nil || r.CheckedAt.After(*last) {
			t := r.CheckedAt
			last = &t
		}
		if now.Sub(r.CheckedAt) > staleness {
			continue
		}
		anyRecent = true
		if r.Success {
			anySuccess = true
			if bestLatency == nil || r.LatencyMS < *bestLatency {
				l := r.LatencyMS
				bestLatency = &l
			}
		}
	}

	switch {
	case anySuccess:
		return StatusUp, bestLatency, last
	case anyRecent:
		return StatusDown, nil, last
	default:
		return StatusUnknown, nil, last
	}
}
