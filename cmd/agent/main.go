package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/agent"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"go.uber.org/zap"
)

type config struct {
	serverAddress  string
	reportInterval intervalValid
	pollInterval   intervalValid
}

func main() {
	cfg, err := resolveConfig()
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	log, err := logger.New(resolveLogLevel())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer log.Sync()

	baseURL := normalizeServerAddr(cfg.serverAddress)
	log.Info("agent starting",
		zap.String("address", baseURL),
		zap.Duration("poll_interval", cfg.pollInterval.interval),
		zap.Duration("report_interval", cfg.reportInterval.interval),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	storage := repository.NewMemStorage()
	newAgent := agent.NewAgent(baseURL, cfg.pollInterval.interval, cfg.reportInterval.interval, storage,
		log.With(zap.String("component", "agent")))
	newAgent.Run(ctx)

	log.Info("agent stopped")
}

// --- config resolution ---

type intervalValid struct {
	interval time.Duration
	isSet    bool
}

func (i *intervalValid) Set(value string) error {
	intervalInt, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("error parsing interval: %w", err)
	}

	if err := i.UpdateToSecond(intervalInt); err != nil {
		return err
	}
	i.isSet = true
	return nil
}

func (i *intervalValid) UpdateToSecond(sec int) error {
	if sec <= 0 {
		return fmt.Errorf("interval must be > 0, got %d", sec)
	}
	i.interval = time.Duration(sec) * time.Second
	return nil
}

func (c config) validation() bool {
	return c.serverAddress != "" && c.reportInterval.isSet && c.pollInterval.isSet
}

func resolveConfig() (*config, error) {
	cfg := &config{}

	// environment variable takes precedence over command line argument
	if envServerAddr, ok := os.LookupEnv("ADDRESS"); ok {
		cfg.serverAddress = envServerAddr
	}

	var reportValid = intervalValid{}
	cfg.reportInterval = reportValid
	if v, ok := os.LookupEnv("REPORT_INTERVAL"); ok {
		err := cfg.reportInterval.Set(v)
		if err != nil {
			return nil, errors.New("error parsing env REPORT_INTERVAL:" + err.Error())
		}
	}

	var pollValid = intervalValid{}
	cfg.pollInterval = pollValid
	if v, ok := os.LookupEnv("POLL_INTERVAL"); ok {
		err := cfg.pollInterval.Set(v)
		if err != nil {
			return nil, errors.New("error parsing env POLL_INTERVAL:" + err.Error())
		}
	}

	if cfg.validation() {
		return cfg, nil
	}

	// parse command line argument
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Agent startup options:")
		fs.PrintDefaults()
	}

	serverAddr := fs.String("a", "localhost:8080", "HTTP server address")
	reportInterval := fs.Int("r", 10, "report interval in seconds")
	pollInterval := fs.Int("p", 2, "poll interval in seconds")

	if err := fs.Parse(normalizeHelpArg(os.Args[1:])); err != nil {
		return nil, err
	}

	if cfg.serverAddress == "" {
		cfg.serverAddress = *serverAddr
	}

	if !cfg.reportInterval.isSet {
		err := cfg.reportInterval.UpdateToSecond(*reportInterval)
		if err != nil {
			return nil, errors.New("failed to set REPORT_INTERVAL from flag:" + err.Error())
		}
	}

	if !cfg.pollInterval.isSet {
		err := cfg.pollInterval.UpdateToSecond(*pollInterval)
		if err != nil {
			return nil, errors.New("failed to set POLL_INTERVAL from flag:" + err.Error())
		}
	}

	return cfg, nil
}

func normalizeHelpArg(args []string) []string {
	normalized := make([]string, len(args))
	copy(normalized, args)
	for i, arg := range normalized {
		if arg == "--help" {
			normalized[i] = "-h"
		}
	}
	return normalized
}

func resolveLogLevel() string {
	if envLogLevel, ok := os.LookupEnv("LOG_LEVEL"); ok {
		return envLogLevel
	}
	return "INFO"
}

func normalizeServerAddr(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}
