// Package config loads the services.yaml file describing what to check.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type CheckType string

const (
	CheckHTTP CheckType = "http"
	CheckTCP  CheckType = "tcp"
	CheckPing CheckType = "ping"
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

type Config struct {
	Groups []Group `yaml:"groups"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	seen := map[string]bool{}
	for _, g := range c.Groups {
		if g.Name == "" {
			return fmt.Errorf("group with empty name")
		}
		for _, s := range g.Services {
			if s.ID == "" {
				return fmt.Errorf("service in group %q has empty id", g.Name)
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
			case CheckHTTP, CheckTCP, CheckPing:
			default:
				return fmt.Errorf("service %q has invalid type %q", s.ID, s.Type)
			}
			if s.Interval <= 0 {
				return fmt.Errorf("service %q has invalid interval_seconds %d", s.ID, s.Interval)
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
