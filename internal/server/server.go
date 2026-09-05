// Package server implements the hub's HTTP API. It exposes internal
// endpoints for agents, the public status API, and the built frontend.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"quorum/internal/config"
	"quorum/internal/store"
)

const (
	historyWindow  = 90 * 24 * time.Hour
	maxHistoryBars = 50
	stalenessMul   = 3 // an agent's check counts as "recent" for up to 3x its interval
)

type Server struct {
	cfg        *config.Config
	st         *store.Store
	agentToken string
	staticDir  string
}

func New(cfg *config.Config, st *store.Store, agentToken, staticDir string) *Server {
	return &Server{cfg: cfg, st: st, agentToken: agentToken, staticDir: staticDir}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/targets", s.authed(s.handleTargets))
	mux.HandleFunc("POST /internal/results", s.authed(s.handleResults))
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /health", s.handleHealth)
	if s.staticDir != "" {
		mux.Handle("/", http.FileServer(http.Dir(s.staticDir)))
	}
	return mux
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

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	var p ResultPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if p.AgentID == "" || p.ServiceID == "" {
		http.Error(w, "agent_id and service_id required", http.StatusBadRequest)
		return
	}
	if p.CheckedAt.IsZero() {
		p.CheckedAt = time.Now()
	}
	if err := s.st.InsertResult(r.Context(), p.ServiceID, p.AgentID, p.CheckedAt, p.Success, p.LatencyMS, p.Error); err != nil {
		log.Printf("insert result: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type statusService struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Status      store.Status       `json:"status"`
	LatencyMS   *int               `json:"latency_ms"`
	UptimePct   *float64           `json:"uptime_pct_90d"`
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
	Groups []statusGroup `json:"groups"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	since := now.Add(-historyWindow)

	resp := statusResponse{}
	for _, g := range s.cfg.Groups {
		sg := statusGroup{Name: g.Name}
		for _, svc := range g.Services {
			rows, err := s.st.RecentRows(r.Context(), svc.ID, since)
			if err != nil {
				log.Printf("recent rows for %s: %v", svc.ID, err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			interval := time.Duration(svc.Interval) * time.Second
			staleness := interval * stalenessMul

			status, latency, lastChecked := store.Aggregate(rows, staleness, now)
			points := store.Bucket(rows, interval, now, maxHistoryBars)
			history := make([]historyPointJSON, len(points))
			for i, p := range points {
				history[i] = historyPointJSON{Time: p.Time, Status: p.Status}
			}

			var uptimePtr *float64
			if pct := store.UptimePercent(rows, interval); pct >= 0 {
				uptimePtr = &pct
			}

			sg.Services = append(sg.Services, statusService{
				ID:          svc.ID,
				Name:        svc.Name,
				Target:      svc.Target,
				Status:      status,
				LatencyMS:   latency,
				UptimePct:   uptimePtr,
				LastChecked: lastChecked,
				History:     history,
			})
		}
		resp.Groups = append(resp.Groups, sg)
	}
	writeJSON(w, resp)
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
