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
	"gophprofile/internal/domain"
	"gophprofile/internal/observability"
	"gophprofile/internal/repository"
	"gophprofile/internal/storage"
	"gophprofile/internal/worker"
	"gophprofile/pkg/db"
	"gophprofile/pkg/retry"
)

// main загружает конфигурацию, обрабатывает сигналы ОС и запускает воркер.
func main() {
	conf, err := config.LoadFlags()
	if err != nil {
		slog.Error("failed to load config", slog.Any("error", err))
		os.Exit(1)
	}

	serviceName := conf.ServiceName
	if serviceName == "gophprofile" {
		serviceName = "gophprofile-worker"
	}
	observability.SetupLogger(serviceName, conf.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, conf, serviceName); err != nil && err != context.Canceled {
		slog.Error("worker stopped with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run подключается к БД, S3 и брокеру и запускает обработку очередей.
func run(ctx context.Context, conf *config.Flags, serviceName string) error {
	otelProvider, err := observability.Setup(ctx, observability.Config{
		ServiceName:  serviceName,
		OTLPEndpoint: conf.OTLPEndpoint,
		OTLPInsecure: conf.OTLPInsecure,
		Enabled:      conf.TracingEnabled,
	})
	if err != nil {
		return fmt.Errorf("otel setup: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelProvider.Shutdown(shutdownCtx); err != nil {
			slog.Error("otel shutdown", slog.Any("error", err))
		}
	}()

	database, err := db.Open(ctx, conf.DatabaseURI)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer database.Close()

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

	if err := observability.StartMetricsServer(ctx, conf.MetricsAddress); err != nil {
		return fmt.Errorf("metrics server: %w", err)
	}

	observability.StartInfraCollector(
		ctx,
		15*time.Second,
		observability.PgxPoolStats{Pool: database},
		rabbit,
		domain.QueueUploaded,
		domain.QueueDeleted,
		domain.QueueProcess,
	)

	repo := repository.NewPostgresAvatarRepository(database)
	w := worker.New(repo, s3, rabbit)
	slog.Info("gophprofile worker started", "metrics_addr", conf.MetricsAddress)
	return w.Run(ctx)
}
