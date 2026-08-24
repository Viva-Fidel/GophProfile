package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// RunMigrations применяет все новые .up.sql миграции из указанной директории через golang-migrate.
func RunMigrations(_ context.Context, databaseURI, dir string) error {
	if databaseURI == "" {
		return fmt.Errorf("empty DATABASE_URI")
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve migrations dir: %w", err)
	}

	dbURL, err := toMigrateDatabaseURL(databaseURI)
	if err != nil {
		return err
	}

	m, err := migrate.New("file://"+absDir, dbURL)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// toMigrateDatabaseURL приводит URI Postgres к схеме pgx5 для golang-migrate.
func toMigrateDatabaseURL(databaseURI string) (string, error) {
	u, err := url.Parse(databaseURI)
	if err != nil {
		return "", fmt.Errorf("parse database uri: %w", err)
	}
	switch u.Scheme {
	case "postgres", "postgresql", "pgx", "pgx5":
		u.Scheme = "pgx5"
	default:
		return "", fmt.Errorf("unsupported database uri scheme %q", u.Scheme)
	}
	return u.String(), nil
}
