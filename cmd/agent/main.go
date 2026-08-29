package main

import (
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/agent"
)

func main() {
	newAgent := agent.NewAgent("http://localhost:8080", 2, 10)
	newAgent.Run()
}
