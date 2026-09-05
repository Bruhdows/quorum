// Package alerts posts to Discord when services go down, when they
// recover, and optionally when they stop reporting. It stays small on
// purpose. One webhook, quorum transitions only, and a per-service cooldown
// so a flapping check can't flood the channel.
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"quorum/internal/config"
	"quorum/internal/store"
)

// Change is one service's status transition, as reported by the hub's
// status watcher.
type Change struct {
	ServiceID string
	Name      string
	From, To  store.Status
}

// Embed colors mirror the dashboard's status palette.
const (
	ColorDown     = 0xDC2626
	ColorRecovery = 0x059669
	ColorUnknown  = 0x71717A
)

// Sender delivers a rendered notification. DiscordNotifier is the real one;
// tests substitute a capture func.
type Sender interface {
	SendAlert(ctx context.Context, title, body string, color int) error
}

// DiscordNotifier posts embeds to a Discord webhook URL.
type DiscordNotifier struct {
	WebhookURL string
	Client     *http.Client // nil uses a default client with a 10s timeout
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Color       int            `json:"color"`
	Timestamp   string         `json:"timestamp"`
	Fields      []discordField `json:"fields"`
}

func (d *DiscordNotifier) SendAlert(ctx context.Context, title, body string, color int) error {
	payload, err := json.Marshal(map[string]any{
		"embeds": []discordEmbed{{
			Title:       title,
			Description: body,
			Color:       color,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Fields:      []discordField{{Name: "Source", Value: "quorum", Inline: true}},
		}},
	})
	if err != nil {
		return err
	}
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// Dispatcher applies the notification policy to a stream of changes:
//   - entering down always notifies;
//   - recovery (down to up) always notifies and resets the cooldown;
//   - unknown transitions only notify when NotifyUnknown is set;
//   - anything else is suppressed while the service is inside its cooldown
//     window, so flapping produces one alert per window, not one per flip.
//
// A failed send gets logged and nothing else. Alerting must never take
// down the hub.
type Dispatcher struct {
	Notifier      Sender
	Cooldown      time.Duration
	NotifyUnknown bool

	mu   sync.Mutex
	last map[string]time.Time
	now  func() time.Time // test hook; nil means time.Now
}

func (d *Dispatcher) clock() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

// FromConfig builds a Dispatcher from cfg, or returns nil when no webhook
// URL is set anywhere. urlOverride (DISCORD_WEBHOOK_URL) wins over the yaml
// value, so the secret stays out of the committed file. cfg must already
// carry defaults, which config.Load guarantees.
func FromConfig(cfg *config.Config, urlOverride string) *Dispatcher {
	url := urlOverride
	if url == "" {
		url = cfg.Alerts.DiscordWebhookURL
	}
	if url == "" {
		return nil
	}
	return &Dispatcher{
		Notifier:      &DiscordNotifier{WebhookURL: url},
		Cooldown:      time.Duration(cfg.Alerts.CooldownMinutes) * time.Minute,
		NotifyUnknown: cfg.Alerts.NotifyUnknown,
		last:          map[string]time.Time{},
	}
}

// Interval returns how often the hub should re-evaluate statuses for
// alerting.
func Interval(cfg *config.Config) time.Duration {
	secs := cfg.Alerts.CheckIntervalSecs
	if secs <= 0 {
		secs = 30
	}
	return time.Duration(secs) * time.Second
}

func (d *Dispatcher) Handle(c Change) {
	var title, body string
	var color int
	recovery := false
	switch {
	case c.To == store.StatusDown:
		title = fmt.Sprintf("🔴 %s is down", c.Name)
		body = "Every checker that reported recently failed to reach it."
		color = ColorDown
	case c.From == store.StatusDown && c.To == store.StatusUp:
		title = fmt.Sprintf("🟢 %s recovered", c.Name)
		body = "A checker reached it again."
		color = ColorRecovery
		recovery = true
	case c.To == store.StatusUnknown && d.NotifyUnknown:
		title = fmt.Sprintf("⚪ %s stopped reporting", c.Name)
		body = "No agent has reported recently."
		color = ColorUnknown
	case c.From == store.StatusUnknown && c.To == store.StatusUp && d.NotifyUnknown:
		title = fmt.Sprintf("🟢 %s is reporting again", c.Name)
		body = "Agents have resumed checks."
		color = ColorRecovery
		recovery = true
	default:
		return
	}

	now := d.clock()
	d.mu.Lock()
	last, seen := d.last[c.ServiceID]
	d.mu.Unlock()
	if !recovery && seen && now.Sub(last) < d.Cooldown {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := d.Notifier.SendAlert(ctx, title, body, color); err != nil {
		log.Printf("alert for %s: %v", c.ServiceID, err)
		return
	}
	d.mu.Lock()
	d.last[c.ServiceID] = now
	d.mu.Unlock()
}
