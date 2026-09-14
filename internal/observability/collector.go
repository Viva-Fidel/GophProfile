package observability

import (
	"context"
	"log/slog"
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
func StartInfraCollector(ctx context.Context, interval time.Duration, pool PoolStats, queues QueueDepthSource, queueNames ...string) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		collect := func() {
			CollectDBStats(pool)
			CollectQueueDepth(ctx, queues, queueNames...)
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

// StartMetricsServer поднимает отдельный HTTP endpoint /metrics.
func StartMetricsServer(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", MetricsHandler())
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("metrics server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}
