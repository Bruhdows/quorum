package store

import (
	"sort"
	"time"
)

type CheckRow struct {
	AgentID   string
	CheckedAt time.Time
	Success   bool
	LatencyMS int
}

// Status is the aggregated state of a service across all agents.
type Status string

const (
	StatusUp      Status = "up"
	StatusDown    Status = "down"
	StatusUnknown Status = "unknown"
)

// Aggregate computes the current status of a service from its agents' most
// recent checks. A service is "up" if at least one agent's latest check
// (within staleness) succeeded, "down" if every agent that has reported
// recently failed, and "unknown" if nothing recent has come in at all.
// It also returns the best (lowest) latency among currently-succeeding
// agents, and the most recent check time overall.
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

type HistoryPoint struct {
	Time   time.Time
	Status Status
}

// Bucket groups rows into fixed-size time windows and marks each bucket up
// if any agent succeeded in it, down if it has data but no successes.
// Empty buckets are omitted. Returns up to maxBuckets most recent buckets,
// oldest first.
func Bucket(rows []CheckRow, bucketSize time.Duration, now time.Time, maxBuckets int) []HistoryPoint {
	if bucketSize <= 0 || maxBuckets <= 0 {
		return nil
	}
	type acc struct {
		anySuccess bool
		any        bool
	}
	buckets := map[int64]*acc{}
	for _, r := range rows {
		idx := r.CheckedAt.Unix() / int64(bucketSize.Seconds())
		b, ok := buckets[idx]
		if !ok {
			b = &acc{}
			buckets[idx] = b
		}
		b.any = true
		if r.Success {
			b.anySuccess = true
		}
	}

	idxs := make([]int64, 0, len(buckets))
	for idx := range buckets {
		idxs = append(idxs, idx)
	}
	sort.Slice(idxs, func(i, j int) bool { return idxs[i] < idxs[j] })

	if len(idxs) > maxBuckets {
		idxs = idxs[len(idxs)-maxBuckets:]
	}

	points := make([]HistoryPoint, 0, len(idxs))
	for _, idx := range idxs {
		st := StatusDown
		if buckets[idx].anySuccess {
			st = StatusUp
		}
		points = append(points, HistoryPoint{
			Time:   time.Unix(idx*int64(bucketSize.Seconds()), 0).UTC(),
			Status: st,
		})
	}
	return points
}

// UptimePercent is the fraction of non-empty buckets that were up, as a
// percentage. Returns -1 if there is no data at all.
func UptimePercent(rows []CheckRow, bucketSize time.Duration) float64 {
	points := Bucket(rows, bucketSize, time.Now(), 1<<30)
	if len(points) == 0 {
		return -1
	}
	ups := 0
	for _, p := range points {
		if p.Status == StatusUp {
			ups++
		}
	}
	return float64(ups) / float64(len(points)) * 100
}
