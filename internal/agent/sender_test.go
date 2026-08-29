package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
)

func TestBuildUpdateURL_EscapesPathSegments(t *testing.T) {
	metricURL, err := buildUpdateURL("http://localhost:8080", models.Gauge, "Heap/Alloc Value", "12.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(metricURL, "/update/gauge/") {
		t.Fatalf("expected update path, got %s", metricURL)
	}

	if !strings.Contains(metricURL, "Heap%2FAlloc%20Value") {
		t.Fatalf("expected escaped metric name in URL, got %s", metricURL)
	}
}

func TestCollectOnceStoresMetrics(t *testing.T) {
	a := NewAgent("http://localhost:8080", 2, 10)

	a.collectOnce()
	a.collectOnce()

	gauges := a.store.ListGuages()
	counters := a.store.ListCounters()

	if _, ok := gauges["Alloc"]; !ok {
		t.Fatalf("expected Alloc gauge to be collected")
	}
	if _, ok := gauges["RandomValue"]; !ok {
		t.Fatalf("expected RandomValue gauge to be collected")
	}
	if got := counters["PollCount"]; got != 2 {
		t.Fatalf("expected PollCount=2 after two collects, got %d", got)
	}
}

func TestReportOnceSendsAllMetrics(t *testing.T) {
	a := NewAgent("http://localhost:8080", 2, 10)
	a.store = repository.NewMemStorage()

	g := 12.5
	c := int64(7)
	_ = a.store.Update(&models.Metrics{ID: "TestGauge", MType: models.Gauge, Value: &g})
	_ = a.store.Update(&models.Metrics{ID: "TestCounter", MType: models.Counter, Delta: &c})

	got := make(map[string]bool)
	var mu sync.Mutex
	var handlerErr error

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method != http.MethodPost {
			handlerErr = fmt.Errorf("expected POST method, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "text/plain" {
			handlerErr = fmt.Errorf("expected Content-Type text/plain, got %s", r.Header.Get("Content-Type"))
		}
		got[r.URL.Path] = true
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a.serverAddr = ts.URL
	a.reportOnce()

	mu.Lock()
	defer mu.Unlock()
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}

	if !got["/update/gauge/TestGauge/12.5"] {
		t.Fatalf("expected gauge request path not received")
	}
	if !got["/update/counter/TestCounter/7"] {
		t.Fatalf("expected counter request path not received")
	}
}
