package observability

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics содержит Prometheus-метрики сервиса.
type Metrics struct {
	gatherer prometheus.Gatherer

	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	UploadsTotal        *prometheus.CounterVec
	UploadDuration      *prometheus.HistogramVec
	DeletesTotal        *prometheus.CounterVec
	StorageBytes        prometheus.Gauge
	ProcessingTotal     *prometheus.CounterVec
	ProcessingDuration  *prometheus.HistogramVec
	DBConnections       *prometheus.GaugeVec
	QueueDepth          *prometheus.GaugeVec
}

// NewMetrics регистрирует метрики в указанном Registerer.
// Если reg == nil, используется prometheus.DefaultRegisterer.
// Для тестов передавайте отдельный *prometheus.Registry.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	gatherer, ok := reg.(prometheus.Gatherer)
	if !ok {
		gatherer = prometheus.DefaultGatherer
	}

	factory := promauto.With(reg)
	return &Metrics{
		gatherer: gatherer,
		HTTPRequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path", "status"},
		),
		UploadsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "avatars_uploads_total",
				Help: "Total number of avatar uploads",
			},
			[]string{"status"},
		),
		UploadDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "avatars_upload_duration_seconds",
				Help:    "Avatar upload duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"status"},
		),
		DeletesTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "avatars_deletes_total",
				Help: "Total number of avatar deletes",
			},
			[]string{"status"},
		),
		StorageBytes: factory.NewGauge(
			prometheus.GaugeOpts{
				Name: "avatars_storage_bytes",
				Help: "Approximate total storage used by avatar originals",
			},
		),
		ProcessingTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "avatars_processing_total",
				Help: "Total number of avatar processing jobs",
			},
			[]string{"status"},
		),
		ProcessingDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "avatars_processing_duration_seconds",
				Help:    "Avatar processing duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"status"},
		),
		DBConnections: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "db_connections",
				Help: "Database connection pool stats",
			},
			[]string{"state"},
		),
		QueueDepth: factory.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "broker_queue_depth",
				Help: "Number of messages in broker queues",
			},
			[]string{"queue"},
		),
	}
}

// Handler возвращает HTTP handler для /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.gatherer, promhttp.HandlerOpts{})
}

// ObserveHTTP записывает RED-метрики HTTP-запроса.
func (m *Metrics) ObserveHTTP(method, path string, status int, d time.Duration) {
	s := httpStatusLabel(status)
	m.HTTPRequestsTotal.WithLabelValues(method, path, s).Inc()
	m.HTTPRequestDuration.WithLabelValues(method, path, s).Observe(d.Seconds())
}

func httpStatusLabel(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

// ObserveUpload записывает бизнес-метрики загрузки.
func (m *Metrics) ObserveUpload(status string, sizeBytes int64, d time.Duration) {
	m.UploadsTotal.WithLabelValues(status).Inc()
	m.UploadDuration.WithLabelValues(status).Observe(d.Seconds())
	if status == "success" && sizeBytes > 0 {
		m.StorageBytes.Add(float64(sizeBytes))
	}
}

// ObserveDelete записывает бизнес-метрики удаления.
func (m *Metrics) ObserveDelete(status string, sizeBytes int64) {
	m.DeletesTotal.WithLabelValues(status).Inc()
	if status == "success" && sizeBytes > 0 {
		m.StorageBytes.Sub(float64(sizeBytes))
	}
}

// ObserveProcessing записывает метрики обработки воркером.
func (m *Metrics) ObserveProcessing(status string, d time.Duration) {
	m.ProcessingTotal.WithLabelValues(status).Inc()
	m.ProcessingDuration.WithLabelValues(status).Observe(d.Seconds())
}

// PoolStats источник статистики пула соединений.
type PoolStats interface {
	Stat() PoolStat
}

// PoolStat — снимок состояния пула.
type PoolStat struct {
	TotalConns    int32
	IdleConns     int32
	AcquiredConns int32
}

// CollectDBStats обновляет gauge соединений БД.
func (m *Metrics) CollectDBStats(stats PoolStats) {
	if stats == nil {
		return
	}
	s := stats.Stat()
	m.DBConnections.WithLabelValues("total").Set(float64(s.TotalConns))
	m.DBConnections.WithLabelValues("idle").Set(float64(s.IdleConns))
	m.DBConnections.WithLabelValues("acquired").Set(float64(s.AcquiredConns))
}

// QueueDepthSource отдаёт число сообщений в очередях.
type QueueDepthSource interface {
	QueueMessageCount(ctx context.Context, queue string) (int, error)
}

// CollectQueueDepth обновляет глубину указанных очередей.
func (m *Metrics) CollectQueueDepth(ctx context.Context, src QueueDepthSource, queues ...string) {
	if src == nil {
		return
	}
	for _, q := range queues {
		n, err := src.QueueMessageCount(ctx, q)
		if err != nil {
			continue
		}
		m.QueueDepth.WithLabelValues(q).Set(float64(n))
	}
}
