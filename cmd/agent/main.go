package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/agent"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/config"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"go.uber.org/zap"
)

func main() {
	cfg, err := resolveConfig()
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	baseURL, err := serverURL(cfg.serverAddress)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	log, err := logger.New(config.LogLevel())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer log.Sync()

	log.Info("agent starting",
		zap.String("address", baseURL),
		zap.Duration("poll_interval", cfg.pollInterval.Duration),
		zap.Duration("report_interval", cfg.reportInterval.Duration),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	storage := repository.NewMemStorage()
	newAgent := agent.NewAgent(baseURL, cfg.pollInterval.Duration, cfg.reportInterval.Duration, storage,
		log.With(zap.String("component", "agent")))
	newAgent.Run(ctx)

	log.Info("agent stopped")
}

// serverURL builds the base URL of the metrics server from the -a / ADDRESS
// value: a bare "host:port" gets the http scheme, an explicit http:// or
// https:// is kept. The result has no trailing slash. A path is allowed, so
// the server can sit behind a reverse proxy prefix.
//
// The scheme is added before parsing: url.Parse("localhost:8080") does not
// fail but reads "localhost" as the scheme.
func serverURL(addr string) (string, error) {
	raw := addr
	if !strings.Contains(addr, "://") {
		raw = "http://" + addr
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid server address %q: %w", addr, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid server address %q: unsupported scheme %q", addr, u.Scheme)
	}
	// Host, not Hostname: ":8080" is valid, the HTTP client dials localhost
	if u.Host == "" {
		return "", fmt.Errorf("invalid server address %q: host is missing", addr)
	}
	return strings.TrimRight(u.String(), "/"), nil
}
