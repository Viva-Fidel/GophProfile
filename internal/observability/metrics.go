package observability

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// HTTPRequestsTotal — число HTTP-запросов.
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration — длительность HTTP-запросов.
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)

	// UploadsTotal — число загрузок аватарок.
	UploadsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "avatars_uploads_total",
			Help: "Total number of avatar uploads",
		},
		[]string{"status"},
	)

	// UploadDuration — длительность загрузки аватарки.
	UploadDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "avatars_upload_duration_seconds",
			Help:    "Avatar upload duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)

	// DeletesTotal — число удалений аватарок.
	DeletesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "avatars_deletes_total",
			Help: "Total number of avatar deletes",
		},
		[]string{"status"},
	)

	// StorageBytes — суммарный объём оригиналов в байтах (приблизительно).
	StorageBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "avatars_storage_bytes",
			Help: "Approximate total storage used by avatar originals",
		},
	)

	// ProcessingTotal — число обработок воркером.
	ProcessingTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "avatars_processing_total",
			Help: "Total number of avatar processing jobs",
		},
		[]string{"status"},
	)

	// ProcessingDuration — длительность обработки аватарки.
	ProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "avatars_processing_duration_seconds",
			Help:    "Avatar processing duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)

	// DBConnections — текущее число соединений с БД.
	DBConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "db_connections",
			Help: "Database connection pool stats",
		},
		[]string{"state"},
	)

	// QueueDepth — глубина очередей RabbitMQ.
	QueueDepth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "broker_queue_depth",
			Help: "Number of messages in broker queues",
		},
		[]string{"queue"},
	)
)

// MetricsHandler возвращает HTTP handler для /metrics.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// ObserveHTTP записывает RED-метрики HTTP-запроса.
func ObserveHTTP(method, path string, status int, d time.Duration) {
	s := httpStatusLabel(status)
	HTTPRequestsTotal.WithLabelValues(method, path, s).Inc()
	HTTPRequestDuration.WithLabelValues(method, path, s).Observe(d.Seconds())
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
func ObserveUpload(status string, sizeBytes int64, d time.Duration) {
	UploadsTotal.WithLabelValues(status).Inc()
	UploadDuration.WithLabelValues(status).Observe(d.Seconds())
	if status == "success" && sizeBytes > 0 {
		StorageBytes.Add(float64(sizeBytes))
	}
}

// ObserveDelete записывает бизнес-метрики удаления.
func ObserveDelete(status string, sizeBytes int64) {
	DeletesTotal.WithLabelValues(status).Inc()
	if status == "success" && sizeBytes > 0 {
		StorageBytes.Sub(float64(sizeBytes))
	}
}

// ObserveProcessing записывает метрики обработки воркером.
func ObserveProcessing(status string, d time.Duration) {
	ProcessingTotal.WithLabelValues(status).Inc()
	ProcessingDuration.WithLabelValues(status).Observe(d.Seconds())
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
func CollectDBStats(stats PoolStats) {
	if stats == nil {
		return
	}
	s := stats.Stat()
	DBConnections.WithLabelValues("total").Set(float64(s.TotalConns))
	DBConnections.WithLabelValues("idle").Set(float64(s.IdleConns))
	DBConnections.WithLabelValues("acquired").Set(float64(s.AcquiredConns))
}

// QueueDepthSource отдаёт число сообщений в очередях.
type QueueDepthSource interface {
	QueueMessageCount(ctx context.Context, queue string) (int, error)
}

// CollectQueueDepth обновляет глубину указанных очередей.
func CollectQueueDepth(ctx context.Context, src QueueDepthSource, queues ...string) {
	if src == nil {
		return
	}
	for _, q := range queues {
		n, err := src.QueueMessageCount(ctx, q)
		if err != nil {
			continue
		}
		QueueDepth.WithLabelValues(q).Set(float64(n))
	}
}
