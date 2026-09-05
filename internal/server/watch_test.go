package server

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"quorum/internal/alerts"
	"quorum/internal/store"
)

// testDBStore opens an isolated scratch database. The store package's own
// tests TRUNCATE the shared test database, and Go runs packages in parallel,
// so sharing it here would make both suites flaky.
func testDBStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	const name = "quorum_watch_test"
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(`DROP DATABASE IF EXISTS "` + name + `"`); err != nil {
		t.Fatalf("drop scratch db: %v", err)
	}
	if _, err := admin.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS "` + name + `"`); err != nil {
			t.Logf("drop scratch db: %v", err)
		}
	})

	u.Path = "/" + name
	st, err := store.Open(u.String())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestWatchStatusesReportsTransitions(t *testing.T) {
	st := testDBStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(testConfig(), st, "secret", "")

	insert := func(success bool) {
		t.Helper()
		err := st.InsertResults(context.Background(), []store.Result{{
			ServiceID: "svc1", AgentID: "a1",
			CheckedAt: time.Now(), Success: success, LatencyMS: 5,
		}})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	changes := make(chan alerts.Change, 10)
	nextChange := func() alerts.Change {
		t.Helper()
		select {
		case c := <-changes:
			return c
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for a status change")
			return alerts.Change{}
		}
	}

	insert(true) // baseline: up
	go s.WatchStatuses(ctx, 20*time.Millisecond, func(c alerts.Change) {
		changes <- c
	})

	// The first poll only sets the baseline: no callbacks for the
	// already-up service.
	select {
	case c := <-changes:
		t.Fatalf("baseline poll should not report, got %+v", c)
	case <-time.After(150 * time.Millisecond):
	}

	time.Sleep(20 * time.Millisecond) // newer timestamp than the baseline row
	insert(false)
	got := nextChange()
	if got.ServiceID != "svc1" || got.From != store.StatusUp || got.To != store.StatusDown {
		t.Errorf("got %+v, want svc1 up->down", got)
	}
	if got.Name != "Svc 1" {
		t.Errorf("got name %q, want the configured service name", got.Name)
	}

	time.Sleep(20 * time.Millisecond)
	insert(true)
	got = nextChange()
	if got.From != store.StatusDown || got.To != store.StatusUp {
		t.Errorf("got %+v, want svc1 down->up", got)
	}
}
