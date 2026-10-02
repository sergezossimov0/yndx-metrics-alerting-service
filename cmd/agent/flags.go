package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

type config struct {
	serverAddress  string
	reportInterval intervalValid
	pollInterval   intervalValid
}

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
