package models

const (
	Counter = "counter"
	Gauge   = "gauge"
)

// Metrics — метрика в плоской модели, без иерархической вложенности структур.
// Delta и Value объявлены через указатели, чтобы отличать значение "0"
// от незаданного значения и не кодировать незаданное поле в JSON.
type Metrics struct {
	ID    string   `json:"id"`
	MType string   `json:"type"`
	Delta *int64   `json:"delta,omitempty"`
	Value *float64 `json:"value,omitempty"`
	Hash  string   `json:"hash,omitempty"`
}

// MetricsList — срез метрик для сериализации через easyjson (снапшот на диске).
//
//easyjson:json
type MetricsList []Metrics
