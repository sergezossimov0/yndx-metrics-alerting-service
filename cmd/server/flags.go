package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/config"
)

type serverConfig struct {
	serverAddress   string
	storeInterval   config.Interval // default 300 seconds, 0 means synchronous writes, negative is invalid
	fileStoragePath string
	restore         bool
	dbConnection    string
}

// ---- resolveConfig reads configuration from command line arguments and environment variables.
// Priority is resolved in one place, in order: built-in values, then flags, then env on top.

func resolveConfig() (*serverConfig, error) {
	cfg := &serverConfig{
		storeInterval: config.Interval{
			Duration:  300 * time.Second,
			AllowZero: true,
		},
	}

	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Server startup options:")
		fs.PrintDefaults()
	}

	fs.StringVar(&cfg.serverAddress, "a", "localhost:8080", "HTTP server address")
	fs.Var(&cfg.storeInterval, "i", "store interval in seconds (0 makes writes synchronous)")
	fs.StringVar(&cfg.fileStoragePath, "f", "metrics-db.json", "file storage path")
	fs.BoolVar(&cfg.restore, "r", true, "restore previously saved metrics on startup")
	fs.StringVar(&cfg.dbConnection, "d", "", "database connection string (DSN)")

	if err := fs.Parse(config.NormalizeHelpArg(os.Args[1:])); err != nil {
		return nil, err
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyEnv overrides flag values with environment variables.
// Declared but empty string variables are treated as unset and keep the flag value;
// empty STORE_INTERVAL and RESTORE are parse errors.
func applyEnv(cfg *serverConfig) error {
	if v, ok := os.LookupEnv("ADDRESS"); ok && v != "" {
		cfg.serverAddress = v
	}

	if v, ok := os.LookupEnv("STORE_INTERVAL"); ok {
		if err := cfg.storeInterval.Set(v); err != nil {
			return fmt.Errorf("failed to set STORE_INTERVAL: %w", err)
		}
	}

	if v, ok := os.LookupEnv("FILE_STORAGE_PATH"); ok && v != "" {
		cfg.fileStoragePath = v
	}

	if v, ok := os.LookupEnv("RESTORE"); ok {
		restore, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("failed to set RESTORE: %w", err)
		}
		cfg.restore = restore
	}

	if v, ok := os.LookupEnv("DATABASE_DSN"); ok && v != "" {
		cfg.dbConnection = v
	}
	return nil
}
