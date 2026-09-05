package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"quorum/internal/server"
)

func TestFetchAndPostResult(t *testing.T) {
	var gotAuth string
	var posted int32

	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/targets", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode([]server.Target{{ID: "svc1", Type: "http", Target: "http://x", Interval: 30}})
	})
	mux.HandleFunc("POST /internal/results", func(w http.ResponseWriter, r *http.Request) {
		// The agent batches, so the body is an array even for a single check.
		var batch []server.ResultPayload
		json.NewDecoder(r.Body).Decode(&batch)
		if len(batch) != 1 {
			t.Errorf("got %d results in batch, want 1", len(batch))
		} else if batch[0].ServiceID != "svc1" || batch[0].AgentID != "agent1" {
			t.Errorf("unexpected payload: %+v", batch[0])
		}
		atomic.AddInt32(&posted, int32(len(batch)))
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	a := New(ts.URL, "secret", "agent1")

	targets, err := a.fetchTargets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("got auth header %q", gotAuth)
	}
	if len(targets) != 1 || targets[0].ID != "svc1" {
		t.Fatalf("got %+v", targets)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.flushLoop(ctx)

	a.runOnce(context.Background(), targets[0])

	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&posted) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&posted) != 1 {
		t.Errorf("expected one result posted, got %d", posted)
	}
}

func TestFlushLoopBatchesResults(t *testing.T) {
	var batches, results int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []server.ResultPayload
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("decode batch: %v", err)
		}
		atomic.AddInt32(&batches, 1)
		atomic.AddInt32(&results, int32(len(batch)))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	a := New(ts.URL, "secret", "agent1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.flushLoop(ctx)

	for i := 0; i < 25; i++ {
		a.results <- server.ResultPayload{AgentID: "agent1", ServiceID: "svc1", Success: true}
	}

	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&results) < 25 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&results); got != 25 {
		t.Fatalf("got %d results, want 25", got)
	}
	// The point of batching: far fewer requests than checks.
	if got := atomic.LoadInt32(&batches); got >= 25 {
		t.Errorf("got %d requests for 25 results, want them grouped", got)
	}
}

func TestQueueFullDropsRatherThanBlocks(t *testing.T) {
	a := New("http://unused", "x", "agent1")
	// No flush loop is running, so the queue fills and stays full.
	for i := 0; i < queueSize; i++ {
		a.results <- server.ResultPayload{ServiceID: "svc1"}
	}

	done := make(chan struct{})
	go func() {
		a.runOnce(context.Background(), server.Target{ID: "svc1", Type: "tcp", Target: "127.0.0.1:1", Interval: 30})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runOnce blocked on a full queue")
	}
}

func TestReconcileStartsAndStopsWatchers(t *testing.T) {
	a := New("http://unused", "x", "agent1")
	running := map[string]context.CancelFunc{}

	a.reconcile(context.Background(), []server.Target{{ID: "a", Interval: 3600}, {ID: "b", Interval: 3600}}, running)
	if len(running) != 2 {
		t.Fatalf("got %d watchers, want 2", len(running))
	}

	// removing "b" from the target list should cancel its watcher
	a.reconcile(context.Background(), []server.Target{{ID: "a", Interval: 3600}}, running)
	if len(running) != 1 {
		t.Fatalf("got %d watchers after removal, want 1", len(running))
	}
	if _, ok := running["a"]; !ok {
		t.Error("watcher for 'a' should still be running")
	}

	time.Sleep(10 * time.Millisecond) // let goroutines exit cleanly
}

func TestReconcileRestartsWatcherOnTargetChange(t *testing.T) {
	a := New("http://unused/", "x", "agent1")
	if a.HubURL != "http://unused" {
		t.Errorf("hub URL should be trimmed of trailing slash, got %q", a.HubURL)
	}
	running := map[string]context.CancelFunc{}
	mk := func(interval int) server.Target {
		// Refused TCP dials fail instantly, so each started watcher produces
		// exactly one queued result right away via its initial runOnce.
		return server.Target{ID: "a", Type: "tcp", Target: "127.0.0.1:1", Interval: interval}
	}
	waitResults := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for len(a.results) < n && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if len(a.results) < n {
			t.Fatalf("got %d check results, want at least %d", len(a.results), n)
		}
	}

	a.reconcile(context.Background(), []server.Target{mk(3600)}, running)
	waitResults(1)

	// Same target again: the watcher must be left alone, so no new check.
	a.reconcile(context.Background(), []server.Target{mk(3600)}, running)
	time.Sleep(100 * time.Millisecond)
	if n := len(a.results); n != 1 {
		t.Fatalf("got %d results after identical reconcile, want 1 (watcher restarted?)", n)
	}

	// Changed interval: the old watcher stops and the new one checks at once.
	a.reconcile(context.Background(), []server.Target{mk(60)}, running)
	waitResults(2)
	if len(running) != 1 {
		t.Fatalf("got %d watchers, want 1", len(running))
	}
}
