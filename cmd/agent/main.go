package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/agent"
)

func main() {
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
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	baseURL := normalizeServerAddr(*serverAddr)
	log.Printf("agent starting with addr=%s poll_interval=%ds report_interval=%ds", baseURL, *pollInterval, *reportInterval)
	newAgent := agent.NewAgent(baseURL, *pollInterval, *reportInterval)
	newAgent.Run()
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
