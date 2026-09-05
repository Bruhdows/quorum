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
	"time"

	"quorum/internal/checker"
	"quorum/internal/config"
	"quorum/internal/server"
)

const refetchInterval = 5 * time.Minute

type Agent struct {
	HubURL  string
	Token   string
	AgentID string
	Client  *http.Client
}

func New(hubURL, token, agentID string) *Agent {
	return &Agent{
		HubURL:  hubURL,
		Token:   token,
		AgentID: agentID,
		Client:  &http.Client{Timeout: 10 * time.Second},
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
		if _, ok := running[t.ID]; ok {
			continue
		}
		tctx, cancel := context.WithCancel(ctx)
		running[t.ID] = cancel
		go a.watch(tctx, t)
	}
	for id, cancel := range running {
		if !seen[id] {
			cancel()
			delete(running, id)
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
	if err := a.postResult(ctx, t.ID, res); err != nil {
		log.Printf("post result for %s: %v", t.ID, err)
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

func (a *Agent) postResult(ctx context.Context, serviceID string, res checker.Result) error {
	payload := server.ResultPayload{
		AgentID:   a.AgentID,
		ServiceID: serviceID,
		CheckedAt: time.Now(),
		Success:   res.Success,
		LatencyMS: res.LatencyMS,
		Error:     res.Error,
	}
	body, err := json.Marshal(payload)
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
