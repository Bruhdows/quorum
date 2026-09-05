// Package agent implements the checker process that runs on each monitoring
// machine. It fetches the target list from the hub, checks each service on
// its own schedule, and posts results back.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"quorum/internal/checker"
	"quorum/internal/config"
	"quorum/internal/server"
)

const (
	refetchInterval = 5 * time.Minute

	// Results are posted in batches. An agent watching a thousand services
	// otherwise opens a thousand HTTP requests per interval; grouping them
	// turns that into one request per flush.
	flushInterval = time.Second
	maxBatch      = 500
	queueSize     = 4096
)

type Agent struct {
	HubURL  string
	Token   string
	AgentID string
	Client  *http.Client

	results chan server.ResultPayload
	// known remembers the target list from the last reconcile so watchers
	// whose type, target, or interval changed can be restarted. Only
	// touched by the Run loop goroutine.
	known map[string]server.Target
}

func New(hubURL, token, agentID string) *Agent {
	return &Agent{
		HubURL:  strings.TrimSuffix(strings.TrimSpace(hubURL), "/"),
		Token:   token,
		AgentID: agentID,
		Client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		results: make(chan server.ResultPayload, queueSize),
		known:   map[string]server.Target{},
	}
}

// Run fetches targets and runs checks forever, refreshing the target list
// periodically so config changes on the hub don't require redeploying agents.
func (a *Agent) Run(ctx context.Context) error {
	running := map[string]context.CancelFunc{}
	defer func() {
		for _, cancel := range running {
			cancel()
		}
	}()

	go a.flushLoop(ctx)

	ticker := time.NewTicker(refetchInterval)
	defer ticker.Stop()

	for {
		targets, err := a.fetchTargets(ctx)
		if err != nil {
			log.Printf("fetch targets: %v", err)
		} else {
			a.reconcile(ctx, targets, running)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *Agent) reconcile(ctx context.Context, targets []server.Target, running map[string]context.CancelFunc) {
	seen := map[string]bool{}
	for _, t := range targets {
		seen[t.ID] = true
		// An interval, type, or target change restarts the watcher. Otherwise
		// the old ticker keeps running on stale settings and the hub-side
		// change never fully lands.
		if prev, ok := a.known[t.ID]; ok && prev == t {
			continue
		}
		if cancel, ok := running[t.ID]; ok {
			cancel()
		}
		tctx, cancel := context.WithCancel(ctx)
		running[t.ID] = cancel
		a.known[t.ID] = t
		go a.watch(tctx, t)
	}
	for id, cancel := range running {
		if !seen[id] {
			cancel()
			delete(running, id)
			delete(a.known, id)
		}
	}
}

func (a *Agent) watch(ctx context.Context, t server.Target) {
	interval := time.Duration(t.Interval) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	a.runOnce(ctx, t)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runOnce(ctx, t)
		}
	}
}

func (a *Agent) runOnce(ctx context.Context, t server.Target) {
	svc := config.Service{Type: config.CheckType(t.Type), Target: t.Target}
	res := checker.Check(ctx, svc)
	payload := server.ResultPayload{
		AgentID:   a.AgentID,
		ServiceID: t.ID,
		CheckedAt: time.Now(),
		Success:   res.Success,
		LatencyMS: res.LatencyMS,
		Error:     res.Error,
	}

	select {
	case a.results <- payload:
	default:
		// A full queue means the hub has been unreachable for a while.
		// Dropping the newest check keeps the agent from growing without
		// bound, and the check repeats on the next tick anyway.
		log.Printf("result queue full, dropping check for %s", t.ID)
	}
}

// flushLoop batches queued results and posts them together.
func (a *Agent) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	batch := make([]server.ResultPayload, 0, maxBatch)
	send := func() {
		if len(batch) == 0 {
			return
		}
		if err := a.postResults(ctx, batch); err != nil {
			log.Printf("post %d results: %v", len(batch), err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Don't drop the final partial batch. ctx is already cancelled
			// here, so postResults gets a fresh context.
			if len(batch) > 0 {
				sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := a.postResults(sendCtx, batch); err != nil {
					log.Printf("post final %d results: %v", len(batch), err)
				}
				cancel()
			}
			return
		case r := <-a.results:
			batch = append(batch, r)
			if len(batch) >= maxBatch {
				send()
			}
		case <-ticker.C:
			send()
		}
	}
}

func (a *Agent) fetchTargets(ctx context.Context) ([]server.Target, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.HubURL+"/internal/targets", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub returned %d", resp.StatusCode)
	}
	var targets []server.Target
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func (a *Agent) postResults(ctx context.Context, batch []server.ResultPayload) error {
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.HubURL+"/internal/results", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("hub returned %d", resp.StatusCode)
	}
	return nil
}
