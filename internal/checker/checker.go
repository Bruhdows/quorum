// Package checker runs a single health check (http, tcp, or ping) and
// reports whether it succeeded and how long it took.
package checker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"time"

	"quorum/internal/config"
)

type Result struct {
	Success   bool
	LatencyMS int
	Error     string
}

var httpClient = &http.Client{Timeout: 5 * time.Second}

func Check(ctx context.Context, s config.Service) Result {
	switch s.Type {
	case config.CheckHTTP:
		return checkHTTP(ctx, s.Target)
	case config.CheckTCP:
		return checkTCP(ctx, s.Target)
	case config.CheckPing:
		return checkPing(ctx, s.Target)
	default:
		return Result{Success: false, Error: fmt.Sprintf("unknown check type %q", s.Type)}
	}
}

func checkHTTP(ctx context.Context, target string) Result {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{Success: false, Error: err.Error()}
	}
	resp, err := httpClient.Do(req)
	latency := time.Since(start)
	if err != nil {
		return Result{Success: false, LatencyMS: int(latency.Milliseconds()), Error: err.Error()}
	}
	defer resp.Body.Close()
	success := resp.StatusCode >= 200 && resp.StatusCode < 400
	res := Result{Success: success, LatencyMS: int(latency.Milliseconds())}
	if !success {
		res.Error = fmt.Sprintf("unexpected status %d", resp.StatusCode)
	}
	return res
}

func checkTCP(ctx context.Context, target string) Result {
	start := time.Now()
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", target)
	latency := time.Since(start)
	if err != nil {
		return Result{Success: false, LatencyMS: int(latency.Milliseconds()), Error: err.Error()}
	}
	conn.Close()
	return Result{Success: true, LatencyMS: int(latency.Milliseconds())}
}

// checkPing shells out to the system ping binary instead of opening a raw
// ICMP socket. This needs the ping binary to have CAP_NET_RAW or the setuid
// bit, which it does on most distros by default. If that assumption ever
// breaks, switch to golang.org/x/net/icmp.
func checkPing(ctx context.Context, target string) Result {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ping", "-c", "1", "-W", "2", target)
	err := cmd.Run()
	latency := time.Since(start)
	if err != nil {
		return Result{Success: false, LatencyMS: int(latency.Milliseconds()), Error: err.Error()}
	}
	return Result{Success: true, LatencyMS: int(latency.Milliseconds())}
}
