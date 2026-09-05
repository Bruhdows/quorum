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
		var p server.ResultPayload
		json.NewDecoder(r.Body).Decode(&p)
		if p.ServiceID != "svc1" || p.AgentID != "agent1" {
			t.Errorf("unexpected payload: %+v", p)
		}
		atomic.AddInt32(&posted, 1)
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

	a.runOnce(context.Background(), targets[0])
	if atomic.LoadInt32(&posted) != 1 {
		t.Errorf("expected one result posted, got %d", posted)
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
