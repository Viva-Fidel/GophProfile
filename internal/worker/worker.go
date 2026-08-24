// Package worker обрабатывает события загрузки и удаления аватарок.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gophprofile/internal/broker"
	"gophprofile/internal/domain"
	"gophprofile/internal/imageutil"
	"gophprofile/internal/repository"
	"gophprofile/internal/storage"
	"gophprofile/pkg/retry"
)

// Consumer читает сообщения из очереди брокера.
type Consumer interface {
	Consume(ctx context.Context, queue string, handler func(broker.Message) error) error
}

// Worker создаёт миниатюры и удаляет файлы из S3.
type Worker struct {
	repo     repository.AvatarRepository
	objects  storage.ObjectStorage
	consumer Consumer
}

// New создаёт воркер обработки событий.
func New(repo repository.AvatarRepository, objects storage.ObjectStorage, consumer Consumer) *Worker {
	return &Worker{repo: repo, objects: objects, consumer: consumer}
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
func (w *Worker) handleUploaded(msg broker.Message) error {
	var event domain.AvatarUploadEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		slog.Error("invalid upload event", slog.Any("error", err))
		return nil
	}
	return w.HandleUploadEvent(context.Background(), event)
}

// handleDeleted разбирает событие удаления и чистит S3.
func (w *Worker) handleDeleted(msg broker.Message) error {
	var event domain.AvatarDeleteEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		slog.Error("invalid delete event", slog.Any("error", err))
		return nil
	}
	return w.HandleDeleteEvent(context.Background(), event)
}

// HandleUploadEvent идемпотентно создаёт миниатюры и обновляет статус.
func (w *Worker) HandleUploadEvent(ctx context.Context, event domain.AvatarUploadEvent) error {
	existing, err := w.repo.GetByID(ctx, event.AvatarID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		return err
	}
	if existing.ProcessingStatus == domain.ProcessingCompleted {
		return nil
	}

	claimed, err := w.repo.ClaimForProcessing(ctx, event.AvatarID)
	if err != nil {
		return err
	}
	if claimed.ProcessingStatus == domain.ProcessingCompleted {
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
			return err
		}
		key := fmt.Sprintf("thumbnails/%s/%s.jpg", event.AvatarID, size.name)
		if err := retry.Do(ctx, 5, 300*time.Millisecond, func() error {
			return w.objects.Upload(ctx, key, resized, "image/jpeg")
		}); err != nil {
			_ = w.repo.MarkProcessingFailed(ctx, event.AvatarID)
			return err
		}
		thumbs[size.name] = key
	}

	return w.repo.UpdateProcessingResult(ctx, event.AvatarID, thumbs, domain.ProcessingCompleted)
}

// HandleDeleteEvent идемпотентно удаляет объекты из S3.
func (w *Worker) HandleDeleteEvent(ctx context.Context, event domain.AvatarDeleteEvent) error {
	return retry.Do(ctx, 5, 300*time.Millisecond, func() error {
		return w.objects.Delete(ctx, event.S3Keys)
	})
}
