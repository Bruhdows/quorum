package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
