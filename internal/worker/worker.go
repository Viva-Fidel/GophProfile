// Package worker обрабатывает события загрузки и удаления аватарок.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"gophprofile/internal/broker"
	"gophprofile/internal/domain"
	"gophprofile/internal/imageutil"
	"gophprofile/internal/observability"
	"gophprofile/internal/storage"
	"gophprofile/pkg/retry"
)

const workerTracerName = "gophprofile/worker"

// AvatarRepository — хранилище метаданных аватарок для воркера.
type AvatarRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Avatar, error)
	ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error)
	UpdateProcessingResult(ctx context.Context, id string, thumbs map[string]string, status string) error
	MarkProcessingFailed(ctx context.Context, id string) error
}

// Consumer читает сообщения из очереди брокера.
type Consumer interface {
	Consume(ctx context.Context, queue string, handler func(context.Context, broker.Message) error) error
}

// Worker создаёт миниатюры и удаляет файлы из S3.
type Worker struct {
	repo     AvatarRepository
	objects  storage.ObjectStorage
	consumer Consumer
	logger   *slog.Logger
	metrics  *observability.Metrics
}

// New создаёт воркер обработки событий.
func New(repo AvatarRepository, objects storage.ObjectStorage, consumer Consumer, logger *slog.Logger, metrics *observability.Metrics) *Worker {
	return &Worker{repo: repo, objects: objects, consumer: consumer, logger: logger, metrics: metrics}
}

// Run подписывается на очереди загрузки и удаления до отмены ctx.
func (w *Worker) Run(ctx context.Context) error {
	errCh := make(chan error, 2)
	go func() {
		errCh <- w.consumer.Consume(ctx, domain.QueueUploaded, w.handleUploaded)
	}()
	go func() {
		errCh <- w.consumer.Consume(ctx, domain.QueueDeleted, w.handleDeleted)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// handleUploaded разбирает событие загрузки и запускает обработку.
func (w *Worker) handleUploaded(ctx context.Context, msg broker.Message) error {
	var event domain.AvatarUploadEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		w.logger.ErrorContext(ctx, "invalid upload event", slog.Any("error", err))
		return nil
	}
	return w.HandleUploadEvent(ctx, event)
}

// handleDeleted разбирает событие удаления и чистит S3.
func (w *Worker) handleDeleted(ctx context.Context, msg broker.Message) error {
	var event domain.AvatarDeleteEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		w.logger.ErrorContext(ctx, "invalid delete event", slog.Any("error", err))
		return nil
	}
	return w.HandleDeleteEvent(ctx, event)
}

// HandleUploadEvent идемпотентно создаёт миниатюры и обновляет статус.
func (w *Worker) HandleUploadEvent(ctx context.Context, event domain.AvatarUploadEvent) error {
	start := time.Now()
	ctx, span := otel.Tracer(workerTracerName).Start(ctx, "process_avatar",
		trace.WithAttributes(
			attribute.String("avatar_id", event.AvatarID),
			attribute.String("user_id", event.UserID),
			attribute.String("s3_key", event.S3Key),
		),
	)
	defer span.End()

	w.logger.InfoContext(ctx, "processing avatar upload",
		"avatar_id", event.AvatarID,
		"user_id", event.UserID,
	)

	existing, err := w.repo.GetByID(ctx, event.AvatarID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			w.metrics.ObserveProcessing("skipped", time.Since(start))
			return nil
		}
		w.metrics.ObserveProcessing("error", time.Since(start))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if existing.ProcessingStatus == domain.ProcessingCompleted {
		w.metrics.ObserveProcessing("skipped", time.Since(start))
		return nil
	}

	claimed, err := w.repo.ClaimForProcessing(ctx, event.AvatarID)
	if err != nil {
		w.metrics.ObserveProcessing("error", time.Since(start))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if claimed.ProcessingStatus == domain.ProcessingCompleted {
		w.metrics.ObserveProcessing("skipped", time.Since(start))
		return nil
	}

	var original []byte
	if err := retry.Do(ctx, 5, 300*time.Millisecond, func() error {
		data, _, downloadErr := w.objects.Download(ctx, event.S3Key)
		if downloadErr != nil {
			return downloadErr
		}
		original = data
		return nil
	}); err != nil {
		_ = w.repo.MarkProcessingFailed(ctx, event.AvatarID)
		w.metrics.ObserveProcessing("error", time.Since(start))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	thumbs := map[string]string{}
	sizes := []struct {
		name string
		w, h int
	}{
		{domain.Size100, 100, 100},
		{domain.Size300, 300, 300},
	}
	for _, size := range sizes {
		resized, err := imageutil.Resize(original, size.w, size.h)
		if err != nil {
			_ = w.repo.MarkProcessingFailed(ctx, event.AvatarID)
			w.metrics.ObserveProcessing("error", time.Since(start))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		key := fmt.Sprintf("thumbnails/%s/%s.jpg", event.AvatarID, size.name)
		if err := retry.Do(ctx, 5, 300*time.Millisecond, func() error {
			return w.objects.Upload(ctx, key, resized, "image/jpeg")
		}); err != nil {
			_ = w.repo.MarkProcessingFailed(ctx, event.AvatarID)
			w.metrics.ObserveProcessing("error", time.Since(start))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		thumbs[size.name] = key
	}

	if err := w.repo.UpdateProcessingResult(ctx, event.AvatarID, thumbs, domain.ProcessingCompleted); err != nil {
		w.metrics.ObserveProcessing("error", time.Since(start))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	w.metrics.ObserveProcessing("success", time.Since(start))
	w.logger.InfoContext(ctx, "avatar processing completed", "avatar_id", event.AvatarID)
	return nil
}

// HandleDeleteEvent идемпотентно удаляет объекты из S3.
func (w *Worker) HandleDeleteEvent(ctx context.Context, event domain.AvatarDeleteEvent) error {
	ctx, span := otel.Tracer(workerTracerName).Start(ctx, "delete_avatar_objects",
		trace.WithAttributes(
			attribute.String("avatar_id", event.AvatarID),
			attribute.Int("s3_keys_count", len(event.S3Keys)),
		),
	)
	defer span.End()

	err := retry.Do(ctx, 5, 300*time.Millisecond, func() error {
		return w.objects.Delete(ctx, event.S3Keys)
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	w.logger.InfoContext(ctx, "avatar objects deleted", "avatar_id", event.AvatarID)
	return nil
}
