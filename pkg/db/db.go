// Package db предоставляет подключение к PostgreSQL и применение SQL-миграций.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Open открывает соединение с PostgreSQL по URI и проверяет доступность БД.
func Open(ctx context.Context, uri string) (*sql.DB, error) {
	if uri == "" {
		return nil, fmt.Errorf("empty DATABASE_URI")
	}

	sqlDB, err := sql.Open("pgx", uri)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := pingWithRetry(ctx, sqlDB, 10, time.Second); err != nil {
		if closeErr := sqlDB.Close(); closeErr != nil {
			slog.ErrorContext(ctx, "close db after ping failure", slog.Any("error", closeErr))
		}
		return nil, err
	}
	return sqlDB, nil
}

// pingWithRetry проверяет доступность БД с несколькими попытками.
func pingWithRetry(ctx context.Context, db *sql.DB, attempts int, delay time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := db.PingContext(ctx); err == nil {
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
