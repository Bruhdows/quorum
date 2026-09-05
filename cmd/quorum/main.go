// Command quorum is both the hub ("serve") and the checker ("agent"),
// selected by the first argument.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"quorum/internal/agent"
	"quorum/internal/alerts"
	"quorum/internal/config"
	"quorum/internal/server"
	"quorum/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "agent":
		runAgent(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: quorum <serve|agent> [flags]")
	os.Exit(1)
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "services.yaml", "path to services.yaml")
	addr := fs.String("addr", ":8080", "listen address")
	staticDir := fs.String("static", "", "path to built frontend (web/dist); empty disables static serving")
	fs.Parse(args)

	token := requireEnv("AGENT_TOKEN")
	dsn := requireEnv("DATABASE_URL")

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	st, err := store.Open(dsn)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	retention := time.Duration(cfg.RetentionDays) * 24 * time.Hour
	go prune(ctx, st, retention)

	srv := server.New(cfg, st, token, *staticDir)
	if disp := alerts.FromConfig(cfg, os.Getenv("DISCORD_WEBHOOK_URL")); disp != nil {
		log.Printf("alerting to Discord enabled (checks every %ds, %dm cooldown)",
			cfg.Alerts.CheckIntervalSecs, cfg.Alerts.CooldownMinutes)
		go srv.WatchStatuses(ctx, alerts.Interval(cfg), disp.Handle)
	} else {
		log.Print("alerting disabled (set alerts.discord_webhook_url or DISCORD_WEBHOOK_URL to enable)")
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("hub listening on %s", *addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

func prune(ctx context.Context, st *store.Store, retention time.Duration) {
	// Prune once at startup too. Otherwise a restart leaves rows older
	// than the window lying around for up to another full day.
	pruneOnce(ctx, st, retention)
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruneOnce(ctx, st, retention)
		}
	}
}

func pruneOnce(ctx context.Context, st *store.Store, retention time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cutoff := time.Now().Add(-retention)
	if err := st.PruneOlderThan(ctx, cutoff); err != nil {
		log.Printf("prune: %v", err)
	}
}

func runAgent(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	hub := fs.String("hub", "", "hub base URL, e.g. https://status.example.com")
	agentID := fs.String("agent-id", "", "unique id for this machine (defaults to hostname)")
	fs.Parse(args)

	if *hub == "" {
		log.Fatal("-hub is required")
	}
	token := requireEnv("AGENT_TOKEN")

	id := *agentID
	if id == "" {
		h, err := os.Hostname()
		if err != nil {
			log.Fatalf("determine hostname: %v", err)
		}
		id = h
	}

	a := agent.New(*hub, token, id)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("agent %q watching hub %s", id, *hub)
	if err := a.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("agent: %v", err)
	}
}

func requireEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("%s environment variable is required", name)
	}
	return v
}
