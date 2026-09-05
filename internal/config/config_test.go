package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const oneService = `
groups:
  - name: g1
    services:
      - id: svc1
        name: Svc 1
        type: http
        target: http://example.com
        interval_seconds: 30
`

func TestSiteDefaultsToQuorum(t *testing.T) {
	cfg, err := Load(write(t, oneService))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Site.Title != "quorum" {
		t.Errorf("got %q, want the default title", cfg.Site.Title)
	}
	if cfg.Site.GitHub != "" {
		t.Errorf("got github %q, want empty by default", cfg.Site.GitHub)
	}
}

func TestSiteIsConfigurable(t *testing.T) {
	cfg, err := Load(write(t, "site:\n  title: Acme\n  github: https://example.com/repo\n"+oneService))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Site.Title != "Acme" || cfg.Site.GitHub != "https://example.com/repo" {
		t.Errorf("got %+v, want the configured values", cfg.Site)
	}
}

func TestTuningDefaults(t *testing.T) {
	cfg, err := Load(write(t, oneService))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RetentionDays != 90 || cfg.StaleMultiplier != 3 {
		t.Errorf("got retention=%d stale=%d, want 90/3", cfg.RetentionDays, cfg.StaleMultiplier)
	}
	if cfg.Alerts.CooldownMinutes != 30 || cfg.Alerts.CheckIntervalSecs != 30 || cfg.Alerts.NotifyUnknown {
		t.Errorf("got alerts %+v, want 30s cooldown, 30s checks, no unknown alerts", cfg.Alerts)
	}
}

func TestTuningRejectsOutOfRange(t *testing.T) {
	bodies := []string{
		"retention_days: 3\n",
		"retention_days: 400\n",
		"stale_multiplier: 99\n",
		"alerts:\n  check_interval_seconds: 1\n",
		"alerts:\n  cooldown_minutes: -5\n",
	}
	for _, body := range bodies {
		if _, err := Load(write(t, body+oneService)); err == nil {
			t.Errorf("config %q should be rejected", body)
		}
	}
}

func TestRejectsBadServiceIDs(t *testing.T) {
	for _, id := range []string{"", "has space", "with/slash", "with?query", strings.Repeat("a", 200)} {
		cfg := &Config{Groups: []Group{{Name: "g", Services: []Service{
			{ID: id, Name: "S", Type: CheckHTTP, Target: "http://example.com", Interval: 30},
		}}}}
		if err := cfg.Validate(); err == nil {
			t.Errorf("id %q should be rejected", id)
		}
	}
}

func TestRejectsOutOfRangeIntervals(t *testing.T) {
	for _, interval := range []int{0, -5, 86401} {
		cfg := &Config{Groups: []Group{{Name: "g", Services: []Service{
			{ID: "svc", Name: "S", Type: CheckHTTP, Target: "http://example.com", Interval: interval},
		}}}}
		if err := cfg.Validate(); err == nil {
			t.Errorf("interval %d should be rejected", interval)
		}
	}
}
