// Package main — точка входа воркера GophProfile.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gophprofile/internal/broker"
	"gophprofile/internal/config"
	"gophprofile/internal/repository"
	"gophprofile/internal/storage"
	"gophprofile/internal/worker"
	"gophprofile/pkg/db"
	"gophprofile/pkg/retry"
)

// main загружает конфигурацию, обрабатывает сигналы ОС и запускает воркер.
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	conf, err := config.LoadFlags()
	if err != nil {
		slog.Error("failed to load config", slog.Any("error", err))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, conf); err != nil && err != context.Canceled {
		slog.Error("worker stopped with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run подключается к БД, S3 и брокеру и запускает обработку очередей.
func run(ctx context.Context, conf *config.Flags) error {
	database, err := db.Open(ctx, conf.DatabaseURI)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			slog.Error("db close", slog.Any("error", err))
		}
	}()

	s3, err := storage.NewS3(conf.S3Endpoint, conf.S3AccessKey, conf.S3SecretKey, conf.S3Bucket, conf.S3UseSSL)
	if err != nil {
		return fmt.Errorf("s3 init: %w", err)
	}
	if err := retry.Do(ctx, 10, time.Second, func() error {
		return s3.EnsureBucket(ctx)
	}); err != nil {
		return fmt.Errorf("s3 bucket: %w", err)
	}

	var rabbit *broker.Rabbit
	if err := retry.Do(ctx, 10, time.Second, func() error {
		var e error
		rabbit, e = broker.NewRabbit(conf.RabbitURI)
		return e
	}); err != nil {
		return fmt.Errorf("rabbitmq init: %w", err)
	}
	defer func() {
		if err := rabbit.Close(); err != nil {
			slog.Error("rabbitmq close", slog.Any("error", err))
		}
	}()

	repo := repository.NewPostgresAvatarRepository(database)
	w := worker.New(repo, s3, rabbit)
	slog.Info("gophprofile worker started")
	return w.Run(ctx)
}
