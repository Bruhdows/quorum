package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// The bucketing and averaging these tests cover live in SQL, so they need a
// real Postgres. Set TEST_DATABASE_URL to run them; CI does. Without it they
// skip, so `go test ./...` still works on a laptop with no database.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := Open(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.db.Exec(`TRUNCATE checks`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return st
}

func seed(t *testing.T, st *Store, results ...Result) {
	t.Helper()
	if err := st.InsertResults(context.Background(), results); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func TestBucketedHistoryAndUptime(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now()

	// Three one-minute buckets: degraded (mixed results), down (both
	// agents failed), up.
	seed(t, st,
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-150 * time.Second), Success: true, LatencyMS: 10},
		Result{ServiceID: "svc", AgentID: "a2", CheckedAt: now.Add(-150 * time.Second), Success: false},
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-90 * time.Second), Success: false},
		Result{ServiceID: "svc", AgentID: "a2", CheckedAt: now.Add(-90 * time.Second), Success: false},
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-30 * time.Second), Success: true, LatencyMS: 20},
	)

	windows := []ServiceWindow{{ID: "svc", BucketSeconds: 60, Since: now.Add(-10 * time.Minute)}}

	history, err := st.BucketedHistory(ctx, windows)
	if err != nil {
		t.Fatal(err)
	}
	points := history["svc"]
	if len(points) != 3 {
		t.Fatalf("got %d buckets, want 3", len(points))
	}
	want := []Status{StatusDegraded, StatusDown, StatusUp}
	for i, w := range want {
		if points[i].Status != w {
			t.Errorf("bucket %d got %s, want %s", i, points[i].Status, w)
		}
	}

	uptime, err := st.UptimeRatios(ctx, windows)
	if err != nil {
		t.Fatal(err)
	}
	if got := uptime["svc"]; got < 66.6 || got > 66.7 {
		t.Errorf("got %.2f%%, want 2 of 3 buckets up", got)
	}
}

func TestUptimeRatiosSkipsServicesWithNoData(t *testing.T) {
	st := testStore(t)
	uptime, err := st.UptimeRatios(context.Background(), []ServiceWindow{
		{ID: "absent", BucketSeconds: 30, Since: time.Now().Add(-time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := uptime["absent"]; ok {
		t.Error("a service with no checks should have no uptime figure")
	}
}

func TestTrendAveragesOnlySuccessfulChecks(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now()

	seed(t, st,
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-90 * time.Second), Success: true, LatencyMS: 10},
		Result{ServiceID: "svc", AgentID: "a2", CheckedAt: now.Add(-90 * time.Second), Success: true, LatencyMS: 20},
		// A failure carries no timing and must not drag the average down.
		Result{ServiceID: "svc", AgentID: "a3", CheckedAt: now.Add(-90 * time.Second), Success: false},
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-30 * time.Second), Success: false},
	)

	points, err := st.Trend(ctx, "svc", now.Add(-10*time.Minute), 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2", len(points))
	}
	if points[0].AvgMS != 15 || points[0].Status != StatusDegraded {
		t.Errorf("first bucket got avg=%d status=%s, want 15/degraded", points[0].AvgMS, points[0].Status)
	}
	if points[1].AvgMS != -1 || points[1].Status != StatusDown {
		t.Errorf("failed bucket got avg=%d status=%s, want -1/down", points[1].AvgMS, points[1].Status)
	}
}

func TestLatencyStatsIgnoreFailures(t *testing.T) {
	st := testStore(t)
	now := time.Now()
	seed(t, st,
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now, Success: true, LatencyMS: 4},
		Result{ServiceID: "svc", AgentID: "a2", CheckedAt: now, Success: true, LatencyMS: 23},
		Result{ServiceID: "svc", AgentID: "a3", CheckedAt: now, Success: false, LatencyMS: 0},
	)

	got, err := st.LatencyStatsSince(context.Background(), "svc", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Samples != 2 || got.MinMS != 4 || got.MaxMS != 23 || got.AvgMS != 14 {
		t.Errorf("got %+v, want avg 14 (13.5 rounded), min 4, max 23, 2 samples", got)
	}
}

func TestStatusChangesReturnsOnlyTransitions(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now()

	// up, up, down, up across four buckets: three events, newest first.
	seed(t, st,
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-210 * time.Second), Success: true, LatencyMS: 1},
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-150 * time.Second), Success: true, LatencyMS: 1},
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-90 * time.Second), Success: false},
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-30 * time.Second), Success: true, LatencyMS: 1},
	)

	events, err := st.StatusChanges(ctx, "svc", now.Add(-10*time.Minute), 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].Status != StatusUp || events[1].Status != StatusDown || events[2].Status != StatusUp {
		t.Errorf("got %+v, want recovery, outage, then the first bucket", events)
	}
	if !events[0].Time.After(events[1].Time) {
		t.Error("events should come back newest first")
	}

	limited, err := st.StatusChanges(ctx, "svc", now.Add(-10*time.Minute), 60, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Errorf("got %d events, want the limit of 2", len(limited))
	}
}

func TestPruneDeletesInBatches(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	now := time.Now()

	// More rows than one prune batch, so the loop has to run more than once.
	var old []Result
	for i := 0; i < pruneBatch+250; i++ {
		old = append(old, Result{
			ServiceID: "svc", AgentID: "a1",
			CheckedAt: now.Add(-100 * 24 * time.Hour), Success: true, LatencyMS: 1,
		})
	}
	seed(t, st, old...)
	seed(t, st, Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now, Success: true, LatencyMS: 1})

	if err := st.PruneOlderThan(ctx, now.Add(-90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	var remaining int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM checks`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("got %d rows left, want only the recent one", remaining)
	}
}

func TestLatestPerAgentTakesNewestOnly(t *testing.T) {
	st := testStore(t)
	now := time.Now()
	seed(t, st,
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now.Add(-time.Minute), Success: false},
		Result{ServiceID: "svc", AgentID: "a1", CheckedAt: now, Success: true, LatencyMS: 7},
		Result{ServiceID: "svc", AgentID: "a2", CheckedAt: now, Success: false},
	)

	latest, err := st.LatestPerAgent(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(latest["svc"]) != 2 {
		t.Fatalf("got %d rows, want one per agent", len(latest["svc"]))
	}
	status, latency, _ := Aggregate(latest["svc"], time.Minute, now)
	if status != StatusUp || latency == nil || *latency != 7 {
		t.Errorf("got status=%s latency=%v, want up/7", status, latency)
	}
}
