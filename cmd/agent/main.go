package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/agent"
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
