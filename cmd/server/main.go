// Package main — точка входа HTTP-сервера GophProfile.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gophprofile/internal/broker"
	"gophprofile/internal/config"
	"gophprofile/internal/domain"
	"gophprofile/internal/handlers"
	"gophprofile/internal/httpserver"
	"gophprofile/internal/observability"
	"gophprofile/internal/repository"
	"gophprofile/internal/services"
	"gophprofile/internal/storage"
	"gophprofile/pkg/circuitbreaker"
	"gophprofile/pkg/db"
	"gophprofile/pkg/retry"
)

// main загружает конфигурацию, обрабатывает сигналы ОС и запускает HTTP-сервер.
func main() {
	conf, err := config.LoadFlags()
	if err != nil {
		slog.Error("failed to load config", slog.Any("error", err))
		os.Exit(1)
	}

	logger := observability.SetupLogger(conf.ServiceName, conf.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, conf, logger); err != nil {
		logger.Error("server stopped with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run подключается к БД, S3 и брокеру, поднимает HTTP API
// и блокируется до отмены ctx либо ошибки Serve.
func run(ctx context.Context, conf *config.Flags, logger *slog.Logger) error {
	otelProvider, err := observability.Setup(ctx, observability.Config{
		ServiceName:  conf.ServiceName,
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

	if err := db.RunMigrations(ctx, conf.DatabaseURI, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	database, err := db.Open(ctx, conf.DatabaseURI)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer database.Close()

	s3Raw, err := storage.NewS3(conf.S3Endpoint, conf.S3AccessKey, conf.S3SecretKey, conf.S3Bucket, conf.S3UseSSL)
	if err != nil {
		return fmt.Errorf("s3 init: %w", err)
	}
	if err := retry.Do(ctx, 10, time.Second, func() error {
		return s3Raw.EnsureBucket(ctx)
	}); err != nil {
		return fmt.Errorf("s3 bucket: %w", err)
	}
	s3 := storage.NewBreakingStorage(s3Raw, circuitbreaker.New(circuitbreaker.Settings{Name: "s3"}))

	var rabbitRaw *broker.Rabbit
	if err := retry.Do(ctx, 10, time.Second, func() error {
		var e error
		rabbitRaw, e = broker.NewRabbit(conf.RabbitURI)
		return e
	}); err != nil {
		return fmt.Errorf("rabbitmq init: %w", err)
	}
	rabbit := broker.NewBreakingRabbit(rabbitRaw, circuitbreaker.New(circuitbreaker.Settings{Name: "rabbitmq"}))
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

	repo := repository.NewBreakingRepository(
		repository.NewPostgresAvatarRepository(database),
		circuitbreaker.New(circuitbreaker.Settings{Name: "postgres"}),
	)
	avatarSvc := services.NewAvatarService(repo, s3, rabbit, conf.PublicURL, logger.With("component", "avatar-service"), metrics)
	healthSvc := services.NewHealthService(database, s3, rabbit)

	handler := httpserver.New(
		handlers.NewAvatarHandler(avatarSvc, logger.With("component", "avatar-handler")),
		handlers.NewHealthHandler(healthSvc),
		"web",
		logger.With("component", "httpserver"),
		metrics,
		conf.RateLimitRPS,
		conf.RateLimitBurst,
	).Router()

	ln, err := net.Listen("tcp", conf.RunAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	httpSrv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	logger.Info("gophprofile started", slog.String("addr", ln.Addr().String()))

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining HTTP connections")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		<-errCh
		logger.Info("gophprofile stopped gracefully")
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http serve: %w", err)
		}
		return nil
	}
}
