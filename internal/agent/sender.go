package agent

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

type metricStore interface {
	Update(metric *models.Metrics) error
	ListGauges() map[string]float64
	ListCounters() map[string]int64
}

type Agent struct {
	store          metricStore
	serverAddr     string
	pollInterval   time.Duration
	reportInterval time.Duration
	client         *http.Client
}

func NewAgent(serverAddr string, pollSec, reportSec int, storage metricStore) *Agent {
	return &Agent{
		store:          storage,
		serverAddr:     serverAddr,
		pollInterval:   time.Duration(pollSec) * time.Second,
		reportInterval: time.Duration(reportSec) * time.Second,
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

func (a *Agent) collectOnce() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	metrics := map[string]float64{
		"Alloc":         float64(m.Alloc),
		"BuckHashSys":   float64(m.BuckHashSys),
		"Frees":         float64(m.Frees),
		"GCCPUFraction": m.GCCPUFraction,
		"GCSys":         float64(m.GCSys),
		"HeapAlloc":     float64(m.HeapAlloc),
		"HeapIdle":      float64(m.HeapIdle),
		"HeapInuse":     float64(m.HeapInuse),
		"HeapObjects":   float64(m.HeapObjects),
		"HeapReleased":  float64(m.HeapReleased),
		"HeapSys":       float64(m.HeapSys),
		"LastGC":        float64(m.LastGC),
		"Lookups":       float64(m.Lookups),
		"MCacheInuse":   float64(m.MCacheInuse),
		"MCacheSys":     float64(m.MCacheSys),
		"MSpanInuse":    float64(m.MSpanInuse),
		"MSpanSys":      float64(m.MSpanSys),
		"Mallocs":       float64(m.Mallocs),
		"NextGC":        float64(m.NextGC),
		"NumForcedGC":   float64(m.NumForcedGC),
		"NumGC":         float64(m.NumGC),
		"OtherSys":      float64(m.OtherSys),
		"PauseTotalNs":  float64(m.PauseTotalNs),
		"StackInuse":    float64(m.StackInuse),
		"StackSys":      float64(m.StackSys),
		"Sys":           float64(m.Sys),
		"TotalAlloc":    float64(m.TotalAlloc),
		"RandomValue":   rand.Float64(),
	}

	for name, value := range metrics {
		gaugeValue := value
		if err := a.store.Update(&models.Metrics{ID: name, MType: models.Gauge, Value: &gaugeValue}); err != nil {
			log.Printf("collect gauge error for %s: %v", name, err)
		}
	}

	pollInc := int64(1)
	if err := a.store.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &pollInc}); err != nil {
		log.Printf("collect counter error for PollCount: %v", err)
	}
}

func buildUpdateURL(base, metricType, name, value string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	baseNoSlash := strings.TrimRight(baseURL.String(), "/")
	return fmt.Sprintf(
		"%s/update/%s/%s/%s",
		baseNoSlash,
		url.PathEscape(metricType),
		url.PathEscape(name),
		url.PathEscape(value),
	), nil
}

func (a *Agent) sendMetric(metricType, name, value string) error {
	metricURL, err := buildUpdateURL(a.serverAddr, metricType, name, value)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, metricURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Printf("close response body error: %v", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: unexpected status %d", metricType, name, resp.StatusCode)
	}
	return nil
}

func (a *Agent) reportOnce() {
	gauges := a.store.ListGauges()
	counters := a.store.ListCounters()

	for name, value := range gauges {
		if err := a.sendMetric(models.Gauge, name, strconv.FormatFloat(value, 'f', -1, 64)); err != nil {
			log.Printf("send gauge error: %v", err)
		}
	}
	for name, value := range counters {
		if err := a.sendMetric(models.Counter, name, strconv.FormatInt(value, 10)); err != nil {
			log.Printf("send counter error: %v", err)
		}
	}
}

func (a *Agent) Run(ctx context.Context) {
	pollTicker := time.NewTicker(a.pollInterval)
	reportTicker := time.NewTicker(a.reportInterval)
	defer pollTicker.Stop()
	defer reportTicker.Stop()

	// first immediate collection
	a.collectOnce()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			a.collectOnce()
		case <-reportTicker.C:
			a.reportOnce()
		}
	}
}
