package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"quorum/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{Groups: []config.Group{
		{Name: "g1", Services: []config.Service{
			{ID: "svc1", Name: "Svc 1", Type: config.CheckHTTP, Target: "http://example.com", Interval: 30},
		}},
	}}
}

func TestTargetsRequiresAuth(t *testing.T) {
	s := New(testConfig(), nil, "secret", "")
	req := httptest.NewRequest(http.MethodGet, "/internal/targets", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 without token", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/internal/targets", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401 with wrong token", rec.Code)
	}
}

func TestTargetsReturnsFlatServiceList(t *testing.T) {
	s := New(testConfig(), nil, "secret", "")
	req := httptest.NewRequest(http.MethodGet, "/internal/targets", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var targets []Target
	if err := json.NewDecoder(rec.Body).Decode(&targets); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].ID != "svc1" {
		t.Fatalf("got %+v, want one target svc1", targets)
	}
}

func TestResultsRejectsBadBody(t *testing.T) {
	s := New(testConfig(), nil, "secret", "")
	req := httptest.NewRequest(http.MethodPost, "/internal/results", strings.NewReader("not json"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for malformed body", rec.Code)
	}
}

func TestResultsRejectsMissingFields(t *testing.T) {
	s := New(testConfig(), nil, "secret", "")
	req := httptest.NewRequest(http.MethodPost, "/internal/results", strings.NewReader(`{"success":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for missing ids", rec.Code)
	}
}

func TestNormalizeResultPayload(t *testing.T) {
	now := time.Now()

	future := normalizeResultPayload(ResultPayload{
		ServiceID: "svc", AgentID: "a",
		CheckedAt: now.Add(time.Hour), Success: true, LatencyMS: 5,
	}, now)
	if !future.CheckedAt.Equal(now) {
		t.Errorf("future timestamp should be clamped to now, got %v", future.CheckedAt)
	}

	zero := normalizeResultPayload(ResultPayload{ServiceID: "svc", AgentID: "a"}, now)
	if !zero.CheckedAt.Equal(now) {
		t.Errorf("zero timestamp should default to now, got %v", zero.CheckedAt)
	}

	neg := normalizeResultPayload(ResultPayload{LatencyMS: -3}, now)
	if neg.LatencyMS != 0 {
		t.Errorf("negative latency should be clamped to 0, got %d", neg.LatencyMS)
	}

	long := normalizeResultPayload(ResultPayload{Error: strings.Repeat("x", maxErrorLen+100)}, now)
	if len(long.Error) != maxErrorLen {
		t.Errorf("error should be truncated to %d chars, got %d", maxErrorLen, len(long.Error))
	}
}

func TestSiteServesConfiguredBranding(t *testing.T) {
	cfg := testConfig()
	cfg.Site = config.Site{Title: "Acme Status", GitHub: "https://github.com/acme/status"}

	s := New(cfg, nil, "secret", "")
	req := httptest.NewRequest(http.MethodGet, "/api/site", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var got siteJSON
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Title != "Acme Status" || got.GitHub != "https://github.com/acme/status" {
		t.Errorf("got %+v, want the configured branding", got)
	}
}

func TestSiteOmitsGithubWhenUnset(t *testing.T) {
	cfg := testConfig()
	cfg.Site = config.Site{Title: "Internal"}

	s := New(cfg, nil, "secret", "")
	req := httptest.NewRequest(http.MethodGet, "/api/site", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	var got siteJSON
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.GitHub != "" {
		t.Errorf("got github %q, want empty so the frontend hides the icon", got.GitHub)
	}
}

func TestIndexServesConfiguredBranding(t *testing.T) {
	dir := t.TempDir()
	raw := `<html><head><title>quorum</title>` +
		`<meta name="description" content="` + config.DefaultSiteDescription + `" />` +
		`<meta property="og:title" content="quorum" />` +
		`<meta property="og:description" content="` + config.DefaultSiteDescription + `" />` +
		`</head><body></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.Site = config.Site{Title: "Fish & Chips", Description: "Hot status, served fresh"}
	s := New(cfg, nil, "secret", dir)

	for _, path := range []string{"/", "/index.html"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, req)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d, want 200", path, rec.Code)
		}
		for _, want := range []string{
			"<title>Fish &amp; Chips</title>",
			`content="Fish &amp; Chips"`,
			"Hot status, served fresh",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: missing %q in %q", path, want, body)
			}
		}
		if strings.Contains(body, "Fish & Chips") {
			t.Errorf("GET %s: unescaped title in %q", path, body)
		}
	}
}
