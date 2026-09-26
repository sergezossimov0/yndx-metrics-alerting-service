package handler

import "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"

// MetricReader and MetricUpdater are declared here, in their consumer, so the
// usecase types do not name them. These compile-time checks keep the usecases
// in sync with what the handlers expect.
var (
	_ MetricReader  = (*usecase.MetricRead)(nil)
	_ MetricUpdater = (*usecase.MetricUpdate)(nil)
)
