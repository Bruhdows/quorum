package store

import (
	"testing"
	"time"
)

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

func TestBucketStatus(t *testing.T) {
	cases := []struct {
		up, hasFailure bool
		want           Status
	}{
		{true, false, StatusUp},
		{true, true, StatusDegraded},
		{false, true, StatusDown},
		{false, false, StatusDown},
	}
	for _, c := range cases {
		if got := BucketStatus(c.up, c.hasFailure); got != c.want {
			t.Errorf("BucketStatus(%v, %v) = %s, want %s", c.up, c.hasFailure, got, c.want)
		}
	}
}
