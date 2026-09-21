// Package main — точка входа воркера GophProfile.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
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
	logger := observability.SetupLogger(serviceName, conf.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, conf, serviceName, logger); err != nil && err != context.Canceled {
		logger.Error("worker stopped with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run подключается к БД, S3 и брокеру и запускает обработку очередей.
func run(ctx context.Context, conf *config.Flags, serviceName string, logger *slog.Logger) error {
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
			logger.Error("otel shutdown", slog.Any("error", err))
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
			logger.Error("rabbitmq close", slog.Any("error", err))
		}
	}()

	metrics := observability.NewMetrics(nil)

	observability.StartInfraCollector(
		ctx,
		15*time.Second,
		metrics,
		observability.PgxPoolStats{Pool: database},
		rabbit,
		domain.QueueUploaded,
		domain.QueueDeleted,
		domain.QueueProcess,
	)

	repo := repository.NewPostgresAvatarRepository(database)
	w := worker.New(repo, s3, rabbit, logger.With("component", "worker"), metrics)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	metricsSrv := observability.NewMetricsServer(conf.MetricsAddress, metrics)
	metricsLogger := logger.With("component", "metrics")

	errCh := make(chan error, 2)
	go func() {
		metricsLogger.Info("metrics server listening", "addr", conf.MetricsAddress)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("metrics server: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		errCh <- w.Run(runCtx)
	}()

	logger.Info("gophprofile worker started", "metrics_addr", conf.MetricsAddress)

	select {
	case <-ctx.Done():
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("metrics shutdown: %w", err)
		}
		<-errCh
		<-errCh
		logger.Info("gophprofile worker stopped")
		return nil
	case err := <-errCh:
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = metricsSrv.Shutdown(shutdownCtx)
		<-errCh
		if err != nil && err != context.Canceled {
			return err
		}
		return nil
	}
}
