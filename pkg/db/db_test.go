package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenEmptyURI(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunMigrationsEmptyURI(t *testing.T) {
	if err := RunMigrations(context.Background(), "", "migrations"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunMigrationsBadScheme(t *testing.T) {
	if err := RunMigrations(context.Background(), "mysql://localhost/db", t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunMigrationsMissingDir(t *testing.T) {
	err := RunMigrations(
		context.Background(),
		"postgres://user:pass@127.0.0.1:1/db?sslmode=disable",
		filepath.Join(t.TempDir(), "missing"),
	)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestToMigrateDatabaseURL(t *testing.T) {
	got, err := toMigrateDatabaseURL("postgres://u:p@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	want := "pgx5://u:p@localhost:5432/db?sslmode=disable"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
