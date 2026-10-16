package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/config"
)

type agentConfig struct {
	serverAddress  string
	reportInterval config.Interval // must be > 0
	pollInterval   config.Interval // must be > 0
}

// resolveConfig reads configuration from command line arguments and environment variables.
// Priority is resolved in one place, in order: built-in values, then flags, then env on top.
func resolveConfig() (*agentConfig, error) {
	cfg := &agentConfig{
		reportInterval: config.Interval{Duration: 10 * time.Second},
		pollInterval:   config.Interval{Duration: 2 * time.Second},
	}

	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Agent startup options:")
		fs.PrintDefaults()
	}

	fs.StringVar(&cfg.serverAddress, "a", "localhost:8080", "HTTP server address")
	fs.Var(&cfg.reportInterval, "r", "report interval in seconds")
	fs.Var(&cfg.pollInterval, "p", "poll interval in seconds")

	if err := fs.Parse(config.NormalizeHelpArg(os.Args[1:])); err != nil {
		return nil, err
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyEnv overrides flag values with environment variables.
// A declared but empty ADDRESS is treated as unset and keeps the flag value;
// empty intervals are parse errors.
func applyEnv(cfg *agentConfig) error {
	if v, ok := os.LookupEnv("ADDRESS"); ok && v != "" {
		cfg.serverAddress = v
	}

	if v, ok := os.LookupEnv("REPORT_INTERVAL"); ok {
		if err := cfg.reportInterval.Set(v); err != nil {
			return fmt.Errorf("failed to set REPORT_INTERVAL: %w", err)
		}
	}

	if v, ok := os.LookupEnv("POLL_INTERVAL"); ok {
		if err := cfg.pollInterval.Set(v); err != nil {
			return fmt.Errorf("failed to set POLL_INTERVAL: %w", err)
		}
	}
	return nil
}
