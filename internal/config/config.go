// Package config loads the services.yaml file describing what to check.
package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

type CheckType string

const (
	CheckHTTP      CheckType = "http"
	CheckTCP       CheckType = "tcp"
	CheckPing      CheckType = "ping"
	CheckMinecraft CheckType = "minecraft"
)

type Service struct {
	ID       string    `yaml:"id"`
	Name     string    `yaml:"name"`
	Type     CheckType `yaml:"type"`
	Target   string    `yaml:"target"`
	Interval int       `yaml:"interval_seconds"`
}

type Group struct {
	Name     string    `yaml:"name"`
	Services []Service `yaml:"services"`
}

// Site is the branding shown in the page header. An empty GitHub URL hides
// the link entirely, for an instance that isn't published anywhere.
type Site struct {
	Title  string `yaml:"title"`
	GitHub string `yaml:"github"`
}

type Config struct {
	Site   Site    `yaml:"site"`
	Groups []Group `yaml:"groups"`

	// Operational tuning. All optional; zero values take the defaults.
	// retention_days bounds both the prune cutoff and the trailing
	// windows (history strip, uptime %), so they can never disagree.
	RetentionDays   int    `yaml:"retention_days"`
	StaleMultiplier int    `yaml:"stale_multiplier"`
	Alerts          Alerts `yaml:"alerts"`
}

// Alerts tunes down/recovery notifications. The webhook URL itself can live
// here or in DISCORD_WEBHOOK_URL (env wins), so the secret stays out of the
// committed config file.
type Alerts struct {
	DiscordWebhookURL string `yaml:"discord_webhook_url"`
	CooldownMinutes   int    `yaml:"cooldown_minutes"`
	CheckIntervalSecs int    `yaml:"check_interval_seconds"`
	NotifyUnknown     bool   `yaml:"notify_unknown"`
}

// Service IDs show up in URLs (/api/services/{id} and ?service=), so they
// only allow characters that survive routing and query strings. The
// interval cap keeps the server's duration math far from int64 overflow.
const (
	minIntervalSeconds = 1
	maxIntervalSeconds = 86400
	maxServiceIDLen    = 128

	defaultRetentionDays   = 90
	minRetentionDays       = 7
	maxRetentionDays       = 365
	defaultStaleMultiplier = 3
	minStaleMultiplier     = 1
	maxStaleMultiplier     = 10
	defaultAlertCooldown   = 30
	defaultAlertCheckSecs  = 30
	minAlertCheckSecs      = 5
)

var serviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Site.Title == "" {
		cfg.Site.Title = "quorum"
	}
	cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// WithDefaults fills in every knob left at zero. Zero means unset
// everywhere, so an explicit 0 can't switch anything off. Out-of-range
// values stay as they are for Validate to reject. Running it twice changes
// nothing.
func (c *Config) WithDefaults() {
	if c.RetentionDays == 0 {
		c.RetentionDays = defaultRetentionDays
	}
	if c.StaleMultiplier == 0 {
		c.StaleMultiplier = defaultStaleMultiplier
	}
	if c.Alerts.CooldownMinutes == 0 {
		c.Alerts.CooldownMinutes = defaultAlertCooldown
	}
	if c.Alerts.CheckIntervalSecs == 0 {
		c.Alerts.CheckIntervalSecs = defaultAlertCheckSecs
	}
}

func (c *Config) Validate() error {
	if c.RetentionDays < minRetentionDays || c.RetentionDays > maxRetentionDays {
		return fmt.Errorf("retention_days %d: want %d..%d", c.RetentionDays, minRetentionDays, maxRetentionDays)
	}
	if c.StaleMultiplier < minStaleMultiplier || c.StaleMultiplier > maxStaleMultiplier {
		return fmt.Errorf("stale_multiplier %d: want %d..%d", c.StaleMultiplier, minStaleMultiplier, maxStaleMultiplier)
	}
	if c.Alerts.CooldownMinutes < 0 {
		return fmt.Errorf("alerts.cooldown_minutes %d: want >= 0", c.Alerts.CooldownMinutes)
	}
	if c.Alerts.CheckIntervalSecs < minAlertCheckSecs {
		return fmt.Errorf("alerts.check_interval_seconds %d: want >= %d", c.Alerts.CheckIntervalSecs, minAlertCheckSecs)
	}
	seen := map[string]bool{}
	for _, g := range c.Groups {
		if g.Name == "" {
			return fmt.Errorf("group with empty name")
		}
		for _, s := range g.Services {
			if !serviceIDPattern.MatchString(s.ID) || len(s.ID) > maxServiceIDLen {
				return fmt.Errorf("service %q has invalid id: use 1-%d chars of letters, digits, dot, underscore, hyphen", s.ID, maxServiceIDLen)
			}
			if seen[s.ID] {
				return fmt.Errorf("duplicate service id %q", s.ID)
			}
			seen[s.ID] = true
			if s.Name == "" {
				return fmt.Errorf("service %q has empty name", s.ID)
			}
			if s.Target == "" {
				return fmt.Errorf("service %q has empty target", s.ID)
			}
			switch s.Type {
			case CheckHTTP, CheckTCP, CheckPing, CheckMinecraft:
			default:
				return fmt.Errorf("service %q has invalid type %q", s.ID, s.Type)
			}
			if s.Interval < minIntervalSeconds || s.Interval > maxIntervalSeconds {
				return fmt.Errorf("service %q has invalid interval_seconds %d: want %d..%d", s.ID, s.Interval, minIntervalSeconds, maxIntervalSeconds)
			}
		}
	}
	return nil
}

// FlatService is a Service with its group name attached, for callers that
// don't care about the group structure.
type FlatService struct {
	Service
	Group string
}

func (c *Config) Flatten() []FlatService {
	var out []FlatService
	for _, g := range c.Groups {
		for _, s := range g.Services {
			out = append(out, FlatService{Service: s, Group: g.Name})
		}
	}
	return out
}
