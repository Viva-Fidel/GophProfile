// Package db предоставляет подключение к PostgreSQL и применение SQL-миграций.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open открывает пул соединений с PostgreSQL по URI и проверяет доступность БД.
func Open(ctx context.Context, uri string) (*pgxpool.Pool, error) {
	if uri == "" {
		return nil, fmt.Errorf("empty DATABASE_URI")
	}

	cfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	cfg.MinConns = 5
	cfg.MaxConnLifetime = time.Hour
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if err := pingWithRetry(ctx, pool, 10, time.Second); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// pingWithRetry проверяет доступность БД с несколькими попытками.
func pingWithRetry(ctx context.Context, pool *pgxpool.Pool, attempts int, delay time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := pool.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("database ping cancelled: %w", ctx.Err())
		}
	}
	return fmt.Errorf("database ping failed after %d attempts: %w", attempts, lastErr)
}
