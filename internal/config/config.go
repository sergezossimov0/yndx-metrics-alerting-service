// Package config holds the building blocks shared by the server and agent
// configurations: interval values, help flag handling and the log level.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Interval is a time interval given in whole seconds on the command line or
// in env. It implements flag.Value, so flags and env share one validation.
type Interval struct {
	Duration  time.Duration
	AllowZero bool // 0 is valid (e.g. synchronous writes); negative never is
}

func (i *Interval) String() string {
	return strconv.Itoa(int(i.Duration / time.Second))
}

func (i *Interval) Set(value string) error {
	sec, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("error parsing interval: %w", err)
	}

	if i.AllowZero && sec < 0 {
		return fmt.Errorf("interval must be >= 0, got %d", sec)
	}
	if !i.AllowZero && sec <= 0 {
		return fmt.Errorf("interval must be > 0, got %d", sec)
	}

	i.Duration = time.Duration(sec) * time.Second
	return nil
}

// NormalizeHelpArg rewrites --help to -h, which the flag package understands.
func NormalizeHelpArg(args []string) []string {
	normalized := make([]string, len(args))
	copy(normalized, args)
	for i, arg := range normalized {
		if arg == "--help" {
			normalized[i] = "-h"
		}
	}
	return normalized
}

// LogLevel returns LOG_LEVEL, or "INFO" when it is not set. A declared but
// empty LOG_LEVEL is passed through: zap parses "" as the info level.
func LogLevel() string {
	if envLogLevel, ok := os.LookupEnv("LOG_LEVEL"); ok {
		return envLogLevel
	}
	return "INFO"
}
