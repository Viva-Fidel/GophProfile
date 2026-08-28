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
	"gophprofile/internal/handlers"
	"gophprofile/internal/httpserver"
	"gophprofile/internal/repository"
	"gophprofile/internal/services"
	"gophprofile/internal/storage"
	"gophprofile/pkg/db"
	"gophprofile/pkg/retry"
)

// main загружает конфигурацию, обрабатывает сигналы ОС и запускает HTTP-сервер.
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	conf, err := config.LoadFlags()
	if err != nil {
		slog.Error("failed to load config", slog.Any("error", err))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, conf); err != nil {
		slog.Error("server stopped with error", slog.Any("error", err))
		os.Exit(1)
	}
}

// run подключается к БД, S3 и брокеру, поднимает HTTP API
// и блокируется до отмены ctx либо ошибки Serve.
func run(ctx context.Context, conf *config.Flags) error {
	if err := db.RunMigrations(ctx, conf.DatabaseURI, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

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

	repo := repository.NewPostgresAvatarRepository(database)
	avatarSvc := services.NewAvatarService(repo, s3, rabbit, conf.PublicURL)
	healthSvc := services.NewHealthService(database, s3, rabbit)

	handler := httpserver.New(
		handlers.NewAvatarHandler(avatarSvc),
		handlers.NewHealthHandler(healthSvc),
		"web",
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

	slog.Info("gophprofile started", slog.String("addr", ln.Addr().String()))

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		<-errCh
		slog.Info("gophprofile stopped")
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http serve: %w", err)
		}
		return nil
	}
}
