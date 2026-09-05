package server

import (
	"context"
	"log"
	"net/http"
	"time"

	"quorum/internal/config"
	"quorum/internal/store"
)

const (
	detailWindow    = 30 * 24 * time.Hour
	maxRecentChecks = 100
	maxEvents       = 20
	maxTrendPoints  = 120
)

// The ranges the detail page offers. Bucket sizes are picked so every range
// comes back as roughly a hundred points, whatever its span.
var detailRanges = []struct {
	Key    string
	Label  string
	Span   time.Duration
	Bucket time.Duration
}{
	{"1h", "Last hour", time.Hour, time.Minute},
	{"24h", "Last 24 hours", 24 * time.Hour, 30 * time.Minute},
	{"7d", "Last 7 days", 7 * 24 * time.Hour, 4 * time.Hour},
	{"30d", "Last 30 days", detailWindow, 24 * time.Hour},
}

type checkJSON struct {
	AgentID   string    `json:"agent_id"`
	Time      time.Time `json:"t"`
	Success   bool      `json:"success"`
	LatencyMS int       `json:"latency_ms"`
	Error     string    `json:"error"`
}

type trendPointJSON struct {
	Time   time.Time    `json:"t"`
	AvgMS  int          `json:"avg_ms"`
	Status store.Status `json:"status"`
}

type rangeJSON struct {
	Key       string           `json:"key"`
	Label     string           `json:"label"`
	UptimePct *float64         `json:"uptime_pct"`
	AvgMS     *int             `json:"avg_latency_ms"`
	Trend     []trendPointJSON `json:"trend"`
}

type eventJSON struct {
	Time   time.Time    `json:"t"`
	Status store.Status `json:"status"`
}

type latencyJSON struct {
	AvgMS   int `json:"avg_ms"`
	MinMS   int `json:"min_ms"`
	MaxMS   int `json:"max_ms"`
	Samples int `json:"samples"`
}

type serviceDetail struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Group       string       `json:"group"`
	Target      string       `json:"target"`
	Type        string       `json:"type"`
	Interval    int          `json:"interval_seconds"`
	Status      store.Status `json:"status"`
	LatencyMS   *int         `json:"latency_ms"`
	LastChecked *time.Time   `json:"last_checked"`
	Latency     latencyJSON  `json:"latency_24h"`
	Ranges      []rangeJSON  `json:"ranges"`
	Checks      []checkJSON  `json:"checks"`
	Events      []eventJSON  `json:"events"`
}

func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var svc *config.FlatService
	for _, fs := range s.cfg.Flatten() {
		if fs.ID == id {
			f := fs
			svc = &f
			break
		}
	}
	if svc == nil {
		http.Error(w, "unknown service", http.StatusNotFound)
		return
	}

	body, err := s.detailCache.get("service:"+id, func() (any, error) {
		return s.buildDetail(r.Context(), *svc)
	})
	if err != nil {
		log.Printf("build detail for %s: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeCached(w, body, detailTTL)
}

// buildDetail asks Postgres for summaries rather than rows. Every query
// here returns one row or a few hundred at most, so the response cost
// stays flat however long the service has been monitored.
func (s *Server) buildDetail(ctx context.Context, svc config.FlatService) (serviceDetail, error) {
	now := time.Now()
	interval := time.Duration(svc.Interval) * time.Second

	latest, err := s.st.LatestPerAgent(ctx, now.Add(-lastCheckedWindow))
	if err != nil {
		return serviceDetail{}, err
	}
	status, latency, lastChecked := store.Aggregate(latest[svc.ID], s.staleness(interval), now)

	out := serviceDetail{
		ID:          svc.ID,
		Name:        svc.Name,
		Group:       svc.Group,
		Target:      svc.Target,
		Type:        string(svc.Type),
		Interval:    svc.Interval,
		Status:      status,
		LatencyMS:   latency,
		LastChecked: lastChecked,
		Ranges:      make([]rangeJSON, 0, len(detailRanges)),
		Checks:      []checkJSON{},
		Events:      []eventJSON{},
	}

	windows := make([]store.ServiceWindow, 0, len(detailRanges))
	for _, d := range detailRanges {
		windows = append(windows, store.ServiceWindow{
			ID:            svc.ID,
			BucketSeconds: int64(svc.Interval),
			Since:         now.Add(-d.Span),
		})
	}

	for i, d := range detailRanges {
		rj := rangeJSON{Key: d.Key, Label: d.Label, Trend: []trendPointJSON{}}
		since := now.Add(-d.Span)

		uptime, err := s.st.UptimeRatios(ctx, windows[i:i+1])
		if err != nil {
			return serviceDetail{}, err
		}
		if pct, ok := uptime[svc.ID]; ok {
			p := pct
			rj.UptimePct = &p
		}

		lat, err := s.st.LatencyStatsSince(ctx, svc.ID, since)
		if err != nil {
			return serviceDetail{}, err
		}
		if lat.Samples > 0 {
			avg := lat.AvgMS
			rj.AvgMS = &avg
		}
		if d.Key == "24h" {
			out.Latency = latencyJSON{AvgMS: lat.AvgMS, MinMS: lat.MinMS, MaxMS: lat.MaxMS, Samples: lat.Samples}
		}

		trend, err := s.st.Trend(ctx, svc.ID, since, int64(d.Bucket.Seconds()), maxTrendPoints)
		if err != nil {
			return serviceDetail{}, err
		}
		for _, p := range trend {
			rj.Trend = append(rj.Trend, trendPointJSON{Time: p.Time, AvgMS: p.AvgMS, Status: p.Status})
		}

		out.Ranges = append(out.Ranges, rj)
	}

	checks, err := s.st.RecentChecks(ctx, svc.ID, maxRecentChecks)
	if err != nil {
		return serviceDetail{}, err
	}
	for _, c := range checks {
		out.Checks = append(out.Checks, checkJSON{
			AgentID:   c.AgentID,
			Time:      c.CheckedAt,
			Success:   c.Success,
			LatencyMS: c.LatencyMS,
			Error:     c.Error,
		})
	}

	// Events use the same bucket as the history strip, so a short outage
	// shows in both. Postgres compares the buckets and returns only the
	// transitions, so a fine bucket costs no extra rows on the wire.
	events, err := s.st.StatusChanges(ctx, svc.ID, now.Add(-detailWindow), int64(svc.Interval), maxEvents)
	if err != nil {
		return serviceDetail{}, err
	}
	for _, e := range events {
		out.Events = append(out.Events, eventJSON{Time: e.Time, Status: e.Status})
	}

	return out, nil
}
