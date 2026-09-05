package store

import "testing"
import "time"

func TestAggregate(t *testing.T) {
	now := time.Now()
	staleness := 90 * time.Second

	t.Run("one agent up out of five is up", func(t *testing.T) {
		rows := []CheckRow{
			{AgentID: "a1", CheckedAt: now, Success: true, LatencyMS: 10},
			{AgentID: "a2", CheckedAt: now, Success: false},
			{AgentID: "a3", CheckedAt: now, Success: false},
			{AgentID: "a4", CheckedAt: now, Success: false},
			{AgentID: "a5", CheckedAt: now, Success: false},
		}
		status, latency, _ := Aggregate(rows, staleness, now)
		if status != StatusUp {
			t.Errorf("got %s, want up", status)
		}
		if latency == nil || *latency != 10 {
			t.Errorf("got latency %v, want 10", latency)
		}
	})

	t.Run("all agents down is down", func(t *testing.T) {
		rows := []CheckRow{
			{AgentID: "a1", CheckedAt: now, Success: false},
			{AgentID: "a2", CheckedAt: now, Success: false},
		}
		status, _, _ := Aggregate(rows, staleness, now)
		if status != StatusDown {
			t.Errorf("got %s, want down", status)
		}
	})

	t.Run("no recent data is unknown", func(t *testing.T) {
		rows := []CheckRow{
			{AgentID: "a1", CheckedAt: now.Add(-1 * time.Hour), Success: true},
		}
		status, _, _ := Aggregate(rows, staleness, now)
		if status != StatusUnknown {
			t.Errorf("got %s, want unknown", status)
		}
	})

	t.Run("stale failure ignored while another agent is fresh and up", func(t *testing.T) {
		rows := []CheckRow{
			{AgentID: "a1", CheckedAt: now.Add(-1 * time.Hour), Success: false},
			{AgentID: "a2", CheckedAt: now, Success: true, LatencyMS: 5},
		}
		status, latency, _ := Aggregate(rows, staleness, now)
		if status != StatusUp || latency == nil || *latency != 5 {
			t.Errorf("got status=%s latency=%v, want up/5", status, latency)
		}
	})

	t.Run("only latest check per agent counts", func(t *testing.T) {
		rows := []CheckRow{
			{AgentID: "a1", CheckedAt: now.Add(-10 * time.Second), Success: false},
			{AgentID: "a1", CheckedAt: now, Success: true, LatencyMS: 7},
		}
		status, latency, _ := Aggregate(rows, staleness, now)
		if status != StatusUp || latency == nil || *latency != 7 {
			t.Errorf("got status=%s latency=%v, want up/7", status, latency)
		}
	})
}

func TestBucketAndUptime(t *testing.T) {
	now := time.Unix(1000000, 0).UTC()
	bucketSize := 30 * time.Second

	rows := []CheckRow{
		{AgentID: "a1", CheckedAt: now.Add(-90 * time.Second), Success: true},
		{AgentID: "a1", CheckedAt: now.Add(-60 * time.Second), Success: false},
		{AgentID: "a2", CheckedAt: now.Add(-60 * time.Second), Success: false},
		{AgentID: "a1", CheckedAt: now.Add(-30 * time.Second), Success: true},
	}

	points := Bucket(rows, bucketSize, now, 10)
	if len(points) != 3 {
		t.Fatalf("got %d buckets, want 3", len(points))
	}
	if points[0].Status != StatusUp {
		t.Errorf("bucket 0 got %s, want up", points[0].Status)
	}
	if points[1].Status != StatusDown {
		t.Errorf("bucket 1 got %s, want down (both agents failed)", points[1].Status)
	}
	if points[2].Status != StatusUp {
		t.Errorf("bucket 2 got %s, want up", points[2].Status)
	}

	pct := UptimePercent(rows, bucketSize)
	want := float64(2) / float64(3) * 100
	if pct != want {
		t.Errorf("got %.4f%%, want %.4f%%", pct, want)
	}
}

func TestUptimePercentNoData(t *testing.T) {
	if got := UptimePercent(nil, time.Second); got != -1 {
		t.Errorf("got %v, want -1 for no data", got)
	}
}
