package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/agent"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
)

type config struct {
	serverAddress  string
	reportInterval int
	pollInterval   int
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

	baseURL := normalizeServerAddr(cfg.serverAddress)
	log.Printf("agent starting with addr=%s poll_interval=%ds report_interval=%ds", baseURL, cfg.pollInterval, cfg.reportInterval)

	storage := repository.NewMemStorage()
	newAgent := agent.NewAgent(baseURL, cfg.pollInterval, cfg.reportInterval, storage)
	newAgent.Run(context.Background())
}

func resolveConfig() (*config, error) {
	cfg := &config{}

	// environment variable takes precedence over command line argument
	var errConvert error
	envServerAddr := os.Getenv("ADDRESS")
	if envServerAddr != "" {
		cfg.serverAddress = envServerAddr
	}

	envReportInterval := os.Getenv("REPORT_INTERVAL")
	if envReportInterval != "" {
		cfg.reportInterval, errConvert = strconv.Atoi(envReportInterval)
	}
	if errConvert != nil {
		return nil, errors.New("Error parsing env REPORT_INTERVAL:" + errConvert.Error())
	}

	envPollInterval := os.Getenv("POLL_INTERVAL")
	if envPollInterval != "" {
		cfg.pollInterval, errConvert = strconv.Atoi(envPollInterval)
	}
	if errConvert != nil {
		return nil, errors.New("Error parsing env POLL_INTERVAL:" + errConvert.Error())
	}

	if cfg.serverAddress != "" && cfg.reportInterval > 0 && cfg.pollInterval > 0 {
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

	if cfg.reportInterval == 0 {
		cfg.reportInterval = *reportInterval
	}

	if cfg.pollInterval == 0 {
		cfg.pollInterval = *pollInterval
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

func normalizeServerAddr(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}
