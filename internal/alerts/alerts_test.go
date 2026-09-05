package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quorum/internal/config"
	"quorum/internal/store"
)

type captureSender struct {
	calls []struct {
		title, body string
		color       int
	}
	err error
}

func (c *captureSender) SendAlert(ctx context.Context, title, body string, color int) error {
	if c.err != nil {
		return c.err
	}
	c.calls = append(c.calls, struct {
		title, body string
		color       int
	}{title, body, color})
	return nil
}

func testDispatcher(sender *captureSender) (*Dispatcher, *time.Time) {
	now := time.Now()
	return &Dispatcher{
		Notifier: sender,
		Cooldown: 30 * time.Minute,
		last:     map[string]time.Time{},
		now:      func() time.Time { return now },
	}, &now
}

func TestDownAndRecoveryFlow(t *testing.T) {
	sender := &captureSender{}
	d, now := testDispatcher(sender)

	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusUp, To: store.StatusDown})
	if len(sender.calls) != 1 || sender.calls[0].color != ColorDown {
		t.Fatalf("down transition should alert, got %+v", sender.calls)
	}
	if !strings.Contains(sender.calls[0].title, "Svc") {
		t.Errorf("alert title should name the service, got %q", sender.calls[0].title)
	}

	// Repeat down inside the cooldown: suppressed.
	*now = now.Add(5 * time.Minute)
	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusUp, To: store.StatusDown})
	if len(sender.calls) != 1 {
		t.Errorf("repeat down inside cooldown should be suppressed, got %d calls", len(sender.calls))
	}

	// Recovery always goes out and resets the window.
	*now = now.Add(5 * time.Minute)
	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusDown, To: store.StatusUp})
	if len(sender.calls) != 2 || sender.calls[1].color != ColorRecovery {
		t.Fatalf("recovery should always notify, got %+v", sender.calls)
	}

	// ...so a fresh outage right after a recovery is inside the new window.
	*now = now.Add(time.Minute)
	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusUp, To: store.StatusDown})
	if len(sender.calls) != 2 {
		t.Errorf("down inside the post-recovery window should be suppressed, got %d calls", len(sender.calls))
	}

	// After the cooldown, alerts flow again.
	*now = now.Add(31 * time.Minute)
	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusUp, To: store.StatusDown})
	if len(sender.calls) != 3 {
		t.Errorf("down after cooldown should notify, got %d calls", len(sender.calls))
	}
}

func TestUnknownGatedByFlag(t *testing.T) {
	sender := &captureSender{}
	d, _ := testDispatcher(sender)

	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusUp, To: store.StatusUnknown})
	if len(sender.calls) != 0 {
		t.Errorf("unknown should be silent by default, got %+v", sender.calls)
	}

	d.NotifyUnknown = true
	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusUp, To: store.StatusUnknown})
	if len(sender.calls) != 1 || sender.calls[0].color != ColorUnknown {
		t.Fatalf("unknown should notify when enabled, got %+v", sender.calls)
	}
}

func TestSendFailureIsNotFatal(t *testing.T) {
	sender := &captureSender{err: errors.New("webhook down")}
	d, _ := testDispatcher(sender)

	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusUp, To: store.StatusDown})
	if len(sender.calls) != 0 {
		t.Error("failed send should not be recorded")
	}
	// A failed send must not arm the cooldown either: the next event after
	// the sender recovers still goes out.
	sender.err = nil
	d.Handle(Change{ServiceID: "svc", Name: "Svc", From: store.StatusUp, To: store.StatusDown})
	if len(sender.calls) != 1 {
		t.Error("alert after a failed send should still go out")
	}
}

func TestFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.WithDefaults()
	if got := FromConfig(cfg, ""); got != nil {
		t.Error("no URL anywhere should disable alerting")
	}
	if got := FromConfig(cfg, "https://discord.example/hook"); got == nil {
		t.Fatal("env override URL should enable alerting")
	} else if got.Cooldown != 30*time.Minute {
		t.Errorf("got cooldown %v, want 30m", got.Cooldown)
	}

	cfg.Alerts.DiscordWebhookURL = "https://discord.example/yaml"
	if got := FromConfig(cfg, "https://discord.example/env"); got == nil {
		t.Fatal("expected a dispatcher")
	} else if n, ok := got.Notifier.(*DiscordNotifier); !ok || n.WebhookURL != "https://discord.example/env" {
		t.Errorf("env should win over yaml, got %+v", got.Notifier)
	}
}

func TestDiscordPayloadShape(t *testing.T) {
	var got struct {
		Embeds []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Color       int    `json:"color"`
			Timestamp   string `json:"timestamp"`
		} `json:"embeds"`
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	n := &DiscordNotifier{WebhookURL: ts.URL}
	if err := n.SendAlert(context.Background(), "🔴 Svc is down", "body", ColorDown); err != nil {
		t.Fatal(err)
	}
	if len(got.Embeds) != 1 || got.Embeds[0].Color != ColorDown || got.Embeds[0].Title == "" {
		t.Errorf("unexpected embed payload: %+v", got.Embeds)
	}
	if _, err := time.Parse(time.RFC3339, got.Embeds[0].Timestamp); err != nil {
		t.Errorf("timestamp %q is not RFC3339", got.Embeds[0].Timestamp)
	}

	n.WebhookURL = ts.URL + "/missing"
	ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if err := n.SendAlert(context.Background(), "t", "b", ColorDown); err == nil {
		t.Error("non-2xx webhook response should be an error")
	}
}
