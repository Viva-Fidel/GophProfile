package observability

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxPoolStats адаптирует *pgxpool.Pool к PoolStats.
type PgxPoolStats struct {
	Pool *pgxpool.Pool
}

// Stat возвращает снимок состояния пула.
func (p PgxPoolStats) Stat() PoolStat {
	if p.Pool == nil {
		return PoolStat{}
	}
	s := p.Pool.Stat()
	return PoolStat{
		TotalConns:    s.TotalConns(),
		IdleConns:     s.IdleConns(),
		AcquiredConns: s.AcquiredConns(),
	}
}

// StartInfraCollector периодически обновляет инфраструктурные метрики.
func StartInfraCollector(ctx context.Context, interval time.Duration, metrics *Metrics, pool PoolStats, queues QueueDepthSource, queueNames ...string) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		collect := func() {
			metrics.CollectDBStats(pool)
			metrics.CollectQueueDepth(ctx, queues, queueNames...)
		}
		collect()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collect()
			}
		}
	}()
}

// NewMetricsServer создаёт HTTP-сервер с endpoint /metrics.
// Жизненный цикл (ListenAndServe / Shutdown) управляется вызывающим кодом.
func NewMetricsServer(addr string, metrics *Metrics) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
