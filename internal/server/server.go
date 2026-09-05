// Package server implements the hub's HTTP API. It exposes internal
// endpoints for agents, the public status API, and the built frontend.
package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"html"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"quorum/internal/alerts"
	"quorum/internal/config"
	"quorum/internal/store"
)

const (
	// One history-strip bar covers a day; retention_days in services.yaml
	// decides how many days back the strip and the uptime % reach.
	dayBucket = 24 * time.Hour

	// How far back to look for an agent's last report. Past this a service
	// reads as "never checked" rather than merely stale.
	lastCheckedWindow = 30 * 24 * time.Hour

	// Every visitor sees the same page, so the answer gets computed once
	// per window however many tabs are open. Trailing uptime barely moves,
	// so it sticks around far longer than the live status.
	statusTTL = 5 * time.Second
	uptimeTTL = time.Minute
	detailTTL = 10 * time.Second

	maxResultsBatch = 500 // per POST to /internal/results

	// Caps a single results POST. A full batch of 500 checks is on the order
	// of 100KB, so 1MB leaves ample headroom while keeping a malicious or
	// malfunctioning agent from forcing the hub to buffer gigabytes.
	maxResultsBytes = 1 << 20

	maxAgentIDLen   = 128
	maxServiceIDLen = 128
	maxErrorLen     = 4096
)

type Server struct {
	cfg         *config.Config
	st          *store.Store
	agentToken  string
	staticDir   string
	statusCache *responseCache
	uptimeCache *responseCache
	detailCache *responseCache
	// indexHTML is web/dist/index.html with the head tags swapped for the
	// configured branding. Nil when static serving is off or the file
	// couldn't be read, in which case the raw files go out untouched.
	indexHTML []byte
}

func New(cfg *config.Config, st *store.Store, agentToken, staticDir string) *Server {
	// Normalize a copy so programmatically built configs (tests, embeds)
	// get the same defaults as loaded ones, without mutating the caller's.
	c := *cfg
	c.WithDefaults()
	s := &Server{
		cfg:         &c,
		st:          st,
		agentToken:  agentToken,
		staticDir:   staticDir,
		statusCache: newResponseCache(statusTTL),
		uptimeCache: newResponseCache(uptimeTTL),
		detailCache: newResponseCache(detailTTL),
	}
	s.indexHTML = loadPatchedIndex(staticDir, c.Site.Title, siteDescription(&c))
	return s
}

// retention returns the trailing window the history strip, uptime %, and
// pruning all agree on.
func (s *Server) retention() time.Duration {
	return time.Duration(s.cfg.RetentionDays) * 24 * time.Hour
}

// staleness returns how long one agent's check counts as recent: a multiple
// of that service's own interval.
func (s *Server) staleness(interval time.Duration) time.Duration {
	return interval * time.Duration(s.cfg.StaleMultiplier)
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/targets", s.authed(s.handleTargets))
	mux.HandleFunc("POST /internal/results", s.authed(s.handleResults))
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/services/{id}", s.handleService)
	mux.HandleFunc("GET /api/site", s.handleSite)
	mux.HandleFunc("GET /health", s.handleHealth)
	if s.staticDir != "" {
		mux.Handle("/", s.serveStatic())
	}
	return mux
}

// Default head tags baked into web/dist/index.html at build time (see
// web/src/pages/index.astro). The hub swaps them for the configured site
// branding so tabs and link unfurls show this instance. Crawlers don't run
// JS, so patching the served HTML is the only way without rebuilding the
// frontend per instance. Whole tags get replaced, never bare words:
// "quorum" also appears in body copy that must stay untouched.
const defaultTitleTag = "<title>quorum</title>"

func siteDescription(c *config.Config) string {
	if c.Site.Description != "" {
		return c.Site.Description
	}
	return config.DefaultSiteDescription
}

// loadPatchedIndex reads the built index.html once and swaps its head tags.
// It returns nil when there is nothing to serve or the file can't be read;
// callers then fall back to the raw file server.
func loadPatchedIndex(dir, title, description string) []byte {
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		return nil
	}
	return patchIndexHead(raw, title, description)
}

func patchIndexHead(page []byte, title, description string) []byte {
	s := string(page)
	s = strings.ReplaceAll(s, defaultTitleTag, "<title>"+html.EscapeString(title)+"</title>")
	s = strings.ReplaceAll(s, `content="quorum"`, `content="`+html.EscapeString(title)+`"`)
	s = strings.ReplaceAll(s, config.DefaultSiteDescription, html.EscapeString(description))
	return []byte(s)
}

// serveStatic serves the built frontend. The build hashes every asset name,
// so those cache forever. The HTML pointing at them must not, or a visitor
// keeps loading the old page after a deploy and asks for files that no
// longer exist. The index page goes out with patched head tags.
func (s *Server) serveStatic() http.Handler {
	fs := http.FileServer(http.Dir(s.staticDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.indexHTML != nil && (r.URL.Path == "/" || r.URL.Path == "/index.html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(s.indexHTML)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_astro/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fs.ServeHTTP(w, r)
	})
}

func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		want := "Bearer " + s.agentToken
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// Target is what an agent needs to know to run one check.
type Target struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Target   string `json:"target"`
	Interval int    `json:"interval_seconds"`
}

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	var out []Target
	for _, fs := range s.cfg.Flatten() {
		out = append(out, Target{
			ID:       fs.ID,
			Type:     string(fs.Type),
			Target:   fs.Target,
			Interval: fs.Interval,
		})
	}
	writeJSON(w, out)
}

// ResultPayload is what an agent posts after running a check.
type ResultPayload struct {
	AgentID   string    `json:"agent_id"`
	ServiceID string    `json:"service_id"`
	CheckedAt time.Time `json:"checked_at"`
	Success   bool      `json:"success"`
	LatencyMS int       `json:"latency_ms"`
	Error     string    `json:"error"`
}

// handleResults accepts either one result or an array of them. Agents batch
// their checks into one POST, and older agents that send a bare object keep
// working.
func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxResultsBytes)
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			http.Error(w, "batch too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "bad request", http.StatusBadRequest)
		}
		return
	}

	var payloads []ResultPayload
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(raw, &payloads); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	} else {
		var one ResultPayload
		if err := json.Unmarshal(raw, &one); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		payloads = []ResultPayload{one}
	}

	if len(payloads) > maxResultsBatch {
		http.Error(w, "too many results in one batch", http.StatusRequestEntityTooLarge)
		return
	}

	now := time.Now()
	results := make([]store.Result, 0, len(payloads))
	for _, p := range payloads {
		if p.AgentID == "" || p.ServiceID == "" {
			http.Error(w, "agent_id and service_id required", http.StatusBadRequest)
			return
		}
		if len(p.AgentID) > maxAgentIDLen || len(p.ServiceID) > maxServiceIDLen {
			http.Error(w, "agent_id or service_id too long", http.StatusBadRequest)
			return
		}
		p = normalizeResultPayload(p, now)
		results = append(results, store.Result{
			ServiceID: p.ServiceID,
			AgentID:   p.AgentID,
			CheckedAt: p.CheckedAt,
			Success:   p.Success,
			LatencyMS: p.LatencyMS,
			Error:     p.Error,
		})
	}

	if err := s.st.InsertResults(r.Context(), results); err != nil {
		log.Printf("insert results: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// normalizeResultPayload clamps the agent-supplied values the hub must
// not trust blindly. Agents stamp CheckedAt from their own clock, so skew
// (or a bad clock) could plant checks in the future that read as recent
// for longer than they should. Error strings get truncated to bound row
// size.
func normalizeResultPayload(p ResultPayload, now time.Time) ResultPayload {
	if p.CheckedAt.IsZero() || p.CheckedAt.After(now) {
		p.CheckedAt = now
	}
	if p.LatencyMS < 0 {
		p.LatencyMS = 0
	}
	if len(p.Error) > maxErrorLen {
		p.Error = p.Error[:maxErrorLen]
	}
	return p
}

type statusService struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Status      store.Status       `json:"status"`
	LatencyMS   *int               `json:"latency_ms"`
	UptimePct   *float64           `json:"uptime_pct"`
	LastChecked *time.Time         `json:"last_checked"`
	History     []historyPointJSON `json:"history"`
	Target      string             `json:"target"`
}

type historyPointJSON struct {
	Time   time.Time    `json:"t"`
	Status store.Status `json:"status"`
}

type statusGroup struct {
	Name     string          `json:"name"`
	Services []statusService `json:"services"`
}

type statusResponse struct {
	Groups     []statusGroup `json:"groups"`
	UptimeDays int           `json:"uptime_days"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	body, err := s.statusCache.get("status", func() (any, error) {
		return s.buildStatus(r.Context())
	})
	if err != nil {
		log.Printf("build status: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeCached(w, body, statusTTL)
}

// buildStatus answers the whole dashboard in three queries, however many
// services exist. It used to run one query per service and reduce the full
// history in Go, so page cost grew with history instead of with the answer.
func (s *Server) buildStatus(ctx context.Context) (statusResponse, error) {
	now := time.Now()
	flat := s.cfg.Flatten()
	retention := s.retention()

	// The strip draws one bar per day over the whole retention window, so
	// every service reads the same window regardless of its check interval.
	histWindows := make([]store.ServiceWindow, 0, len(flat))
	for _, fs := range flat {
		histWindows = append(histWindows, store.ServiceWindow{
			ID:            fs.ID,
			BucketSeconds: int64(dayBucket.Seconds()),
			Since:         now.Add(-retention),
		})
	}

	history, err := s.st.BucketedHistory(ctx, histWindows)
	if err != nil {
		return statusResponse{}, err
	}
	latest, err := s.st.LatestPerAgent(ctx, now.Add(-lastCheckedWindow))
	if err != nil {
		return statusResponse{}, err
	}
	uptime, err := s.uptimePercents(ctx, now, flat)
	if err != nil {
		return statusResponse{}, err
	}

	resp := statusResponse{UptimeDays: s.cfg.RetentionDays}
	bars := s.cfg.RetentionDays
	for _, g := range s.cfg.Groups {
		sg := statusGroup{Name: g.Name}
		for _, svc := range g.Services {
			interval := time.Duration(svc.Interval) * time.Second
			status, latency, lastChecked := store.Aggregate(latest[svc.ID], s.staleness(interval), now)

			points := history[svc.ID]
			if len(points) > bars {
				points = points[len(points)-bars:]
			}
			dayBars := make([]historyPointJSON, len(points))
			for i, p := range points {
				dayBars[i] = historyPointJSON{Time: p.Time, Status: p.Status}
			}

			var uptimePtr *float64
			if pct, ok := uptime[svc.ID]; ok {
				p := pct
				uptimePtr = &p
			}

			sg.Services = append(sg.Services, statusService{
				ID:          svc.ID,
				Name:        svc.Name,
				Target:      svc.Target,
				Status:      status,
				LatencyMS:   latency,
				UptimePct:   uptimePtr,
				LastChecked: lastChecked,
				History:     dayBars,
			})
		}
		resp.Groups = append(resp.Groups, sg)
	}
	return resp, nil
}

// This is the one query that still scans the full retention window, so it
// gets its own longer-lived cache entry. A trailing average barely moves
// between two page loads.
func (s *Server) uptimePercents(ctx context.Context, now time.Time, flat []config.FlatService) (map[string]float64, error) {
	body, err := s.uptimeCache.get("uptime", func() (any, error) {
		windows := make([]store.ServiceWindow, 0, len(flat))
		for _, fs := range flat {
			windows = append(windows, store.ServiceWindow{
				ID:            fs.ID,
				BucketSeconds: int64(fs.Interval),
				Since:         now.Add(-s.retention()),
			})
		}
		return s.st.UptimeRatios(ctx, windows)
	})
	if err != nil {
		return nil, err
	}
	var out map[string]float64
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type siteJSON struct {
	Title  string `json:"title"`
	GitHub string `json:"github"`
}

// handleSite serves the header branding. It comes from services.yaml so the
// title and the link can change without rebuilding the frontend.
func (s *Server) handleSite(w http.ResponseWriter, r *http.Request) {
	body, err := json.Marshal(siteJSON{Title: s.cfg.Site.Title, GitHub: s.cfg.Site.GitHub})
	if err != nil {
		log.Printf("encode site: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeCached(w, body, time.Minute)
}

// handleHealth reports whether the hub can reach its database, for use by
// load balancers and container orchestrators.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Ping(r.Context()); err != nil {
		http.Error(w, "database unreachable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// currentStatuses resolves every configured service to its live quorum
// status with one LatestPerAgent query. The dashboard and the alert watcher
// both read this. The history strip is display only.
func (s *Server) currentStatuses(ctx context.Context) (map[string]alerts.Change, error) {
	now := time.Now()
	flat := s.cfg.Flatten()
	latest, err := s.st.LatestPerAgent(ctx, now.Add(-lastCheckedWindow))
	if err != nil {
		return nil, err
	}
	out := make(map[string]alerts.Change, len(flat))
	for _, fs := range flat {
		interval := time.Duration(fs.Interval) * time.Second
		status, _, _ := store.Aggregate(latest[fs.ID], s.staleness(interval), now)
		out[fs.ID] = alerts.Change{ServiceID: fs.ID, Name: fs.Name, To: status}
	}
	return out, nil
}

// WatchStatuses polls the live quorum status and reports every transition
// to onChange. The first poll only sets the baseline, so a hub restart
// never re-announces the current state. DB errors get logged and retried
// on the next tick without disturbing the baseline.
func (s *Server) WatchStatuses(ctx context.Context, interval time.Duration, onChange func(alerts.Change)) {
	var last map[string]alerts.Change
	poll := func() {
		cur, err := s.currentStatuses(ctx)
		if err != nil {
			log.Printf("watch statuses: %v", err)
			return
		}
		if last == nil {
			last = cur
			return
		}
		for id, st := range cur {
			if prev, ok := last[id]; ok && prev.To != st.To {
				st.From = prev.To
				onChange(st)
			}
		}
		last = cur
	}
	poll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}
