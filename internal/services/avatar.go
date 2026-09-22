// Package services содержит бизнес-логику управления аватарками.
package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
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

const avatarTracerName = "gophprofile/avatar-service"

// AvatarRepository — хранилище метаданных аватарок для сервиса.
type AvatarRepository interface {
	Create(ctx context.Context, a domain.Avatar) error
	GetByID(ctx context.Context, id string) (*domain.Avatar, error)
	GetLatestByUserID(ctx context.Context, userID string) (*domain.Avatar, error)
	ListByUserID(ctx context.Context, userID string) ([]domain.Avatar, error)
	SoftDelete(ctx context.Context, id string) error
}

// AvatarService оркестрирует валидацию, S3, БД и события брокера.
type AvatarService struct {
	repo      AvatarRepository
	objects   storage.ObjectStorage
	publisher broker.Publisher
	publicURL string
	logger    *slog.Logger
	metrics   *observability.Metrics
}

// NewAvatarService создаёт сервис аватарок.
func NewAvatarService(repo AvatarRepository, objects storage.ObjectStorage, publisher broker.Publisher, publicURL string, logger *slog.Logger, metrics *observability.Metrics) *AvatarService {
	return &AvatarService{
		repo:      repo,
		objects:   objects,
		publisher: publisher,
		publicURL: strings.TrimRight(publicURL, "/"),
		logger:    logger,
		metrics:   metrics,
	}
}

// Upload валидирует файл, сохраняет оригинал в S3, пишет метаданные и публикует событие.
func (s *AvatarService) Upload(ctx context.Context, userID, fileName string, data []byte) (*domain.Avatar, error) {
	start := time.Now()
	ctx, span := otel.Tracer(avatarTracerName).Start(ctx, "upload_avatar",
		trace.WithAttributes(
			attribute.String("user_id", userID),
			attribute.String("file_name", fileName),
			attribute.Int64("file_size", int64(len(data))),
		),
	)
	defer span.End()

	logger := s.logger.With(
		"user_id", userID,
		"file_name", fileName,
		"file_size", len(data),
	)
	logger.InfoContext(ctx, "uploading avatar")

	if userID == "" {
		s.metrics.ObserveUpload("error", 0, time.Since(start))
		span.SetStatus(codes.Error, "missing user")
		return nil, domain.ErrMissingUser
	}
	if len(data) == 0 {
		s.metrics.ObserveUpload("error", 0, time.Since(start))
		span.SetStatus(codes.Error, "missing file")
		return nil, domain.ErrMissingFile
	}
	if int64(len(data)) > domain.MaxUploadBytes {
		s.metrics.ObserveUpload("error", 0, time.Since(start))
		span.SetStatus(codes.Error, "too large")
		return nil, domain.ErrTooLarge
	}

	mime, width, height, err := imageutil.Detect(data)
	if err != nil {
		s.metrics.ObserveUpload("error", 0, time.Since(start))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(attribute.String("mime_type", mime))

	id := uuid.NewString()
	now := time.Now().UTC()
	s3Key := fmt.Sprintf("originals/%s/%s/%s", userID, id, sanitizeFileName(fileName))

	if err := s.objects.Upload(ctx, s3Key, data, mime); err != nil {
		s.metrics.ObserveUpload("error", 0, time.Since(start))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	avatar := domain.Avatar{
		ID:               id,
		UserID:           userID,
		FileName:         fileName,
		MimeType:         mime,
		SizeBytes:        int64(len(data)),
		Width:            width,
		Height:           height,
		S3Key:            s3Key,
		ThumbnailS3Keys:  map[string]string{},
		UploadStatus:     domain.UploadStatusUploaded,
		ProcessingStatus: domain.ProcessingPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.repo.Create(ctx, avatar); err != nil {
		s.metrics.ObserveUpload("error", 0, time.Since(start))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	event := domain.AvatarUploadEvent{AvatarID: id, UserID: userID, S3Key: s3Key}
	body, err := json.Marshal(event)
	if err != nil {
		s.metrics.ObserveUpload("error", 0, time.Since(start))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if err := retry.Do(ctx, 3, 200*time.Millisecond, func() error {
		return s.publisher.Publish(ctx, domain.RoutingUploaded, "upload:"+id, body)
	}); err != nil {
		s.logger.ErrorContext(ctx, "publish upload event", slog.String("avatar_id", id), slog.Any("error", err))
	}

	s.metrics.ObserveUpload("success", avatar.SizeBytes, time.Since(start))
	logger.InfoContext(ctx, "avatar uploaded", "avatar_id", id, "mime_type", mime)
	return &avatar, nil
}

// Get возвращает аватарку по id.
func (s *AvatarService) Get(ctx context.Context, id string) (*domain.Avatar, error) {
	ctx, span := otel.Tracer(avatarTracerName).Start(ctx, "get_avatar",
		trace.WithAttributes(attribute.String("avatar_id", id)),
	)
	defer span.End()
	return s.repo.GetByID(ctx, id)
}

// GetLatestByUser возвращает последнюю аватарку пользователя.
func (s *AvatarService) GetLatestByUser(ctx context.Context, userID string) (*domain.Avatar, error) {
	ctx, span := otel.Tracer(avatarTracerName).Start(ctx, "get_latest_avatar",
		trace.WithAttributes(attribute.String("user_id", userID)),
	)
	defer span.End()
	return s.repo.GetLatestByUserID(ctx, userID)
}

// ListByUser возвращает список аватарок пользователя.
func (s *AvatarService) ListByUser(ctx context.Context, userID string) ([]domain.Avatar, error) {
	ctx, span := otel.Tracer(avatarTracerName).Start(ctx, "list_avatars",
		trace.WithAttributes(attribute.String("user_id", userID)),
	)
	defer span.End()

	items, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if items == nil {
		return []domain.Avatar{}, nil
	}
	return items, nil
}

// File отдаёт байты изображения нужного размера и формата.
func (s *AvatarService) File(ctx context.Context, avatar *domain.Avatar, size, format string) (data []byte, contentType, etag string, err error) {
	ctx, span := otel.Tracer(avatarTracerName).Start(ctx, "get_avatar_file",
		trace.WithAttributes(
			attribute.String("avatar_id", avatar.ID),
			attribute.String("size", size),
			attribute.String("format", format),
		),
	)
	defer span.End()

	_, _, original, err := imageutil.ParseSize(size)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, "", "", err
	}

	key := avatar.S3Key
	if !original {
		thumbKey, ok := avatar.ThumbnailKey(size)
		if ok {
			key = thumbKey
		}
	}

	data, contentType, err = s.objects.Download(ctx, key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, "", "", err
	}
	if contentType == "" {
		contentType = avatar.MimeType
	}

	if format != "" {
		converted, mime, convErr := imageutil.Convert(data, format)
		if convErr != nil {
			span.RecordError(convErr)
			span.SetStatus(codes.Error, convErr.Error())
			return nil, "", "", convErr
		}
		data = converted
		contentType = mime
	}

	sum := sha256.Sum256(append([]byte(avatar.ID+"|"+size+"|"+format+"|"), data...))
	etag = `"` + hex.EncodeToString(sum[:8]) + `"`
	return data, contentType, etag, nil
}

// Delete мягко удаляет аватарку, если actor — её владелец.
func (s *AvatarService) Delete(ctx context.Context, actorUserID, avatarID string) error {
	ctx, span := otel.Tracer(avatarTracerName).Start(ctx, "delete_avatar",
		trace.WithAttributes(
			attribute.String("actor_user_id", actorUserID),
			attribute.String("avatar_id", avatarID),
		),
	)
	defer span.End()

	if actorUserID == "" {
		s.metrics.ObserveDelete("error", 0)
		return domain.ErrMissingUser
	}
	avatar, err := s.repo.GetByID(ctx, avatarID)
	if err != nil {
		s.metrics.ObserveDelete("error", 0)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if avatar.UserID != actorUserID {
		s.metrics.ObserveDelete("error", 0)
		return domain.ErrForbidden
	}
	return s.softDeleteAndPublish(ctx, avatar)
}

// DeleteUserAvatar мягко удаляет текущую аватарку пользователя.
func (s *AvatarService) DeleteUserAvatar(ctx context.Context, actorUserID, userID string) error {
	ctx, span := otel.Tracer(avatarTracerName).Start(ctx, "delete_user_avatar",
		trace.WithAttributes(
			attribute.String("actor_user_id", actorUserID),
			attribute.String("user_id", userID),
		),
	)
	defer span.End()

	if actorUserID == "" {
		s.metrics.ObserveDelete("error", 0)
		return domain.ErrMissingUser
	}
	if actorUserID != userID {
		s.metrics.ObserveDelete("error", 0)
		return domain.ErrForbidden
	}
	avatar, err := s.repo.GetLatestByUserID(ctx, userID)
	if err != nil {
		s.metrics.ObserveDelete("error", 0)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return s.softDeleteAndPublish(ctx, avatar)
}

// AvatarURL собирает публичный URL оригинала.
func (s *AvatarService) AvatarURL(id string) string {
	return s.publicURL + "/api/v1/avatars/" + id
}

// ThumbnailURL собирает публичный URL миниатюры.
func (s *AvatarService) ThumbnailURL(id, size string) string {
	return s.AvatarURL(id) + "?size=" + size
}

// softDeleteAndPublish помечает запись удалённой и публикует событие очистки S3.
func (s *AvatarService) softDeleteAndPublish(ctx context.Context, avatar *domain.Avatar) error {
	if err := s.repo.SoftDelete(ctx, avatar.ID); err != nil {
		s.metrics.ObserveDelete("error", 0)
		return err
	}
	keys := []string{avatar.S3Key}
	for _, k := range avatar.ThumbnailS3Keys {
		if k != "" {
			keys = append(keys, k)
		}
	}
	event := domain.AvatarDeleteEvent{AvatarID: avatar.ID, S3Keys: keys}
	body, err := json.Marshal(event)
	if err != nil {
		s.metrics.ObserveDelete("error", 0)
		return err
	}
	if err := retry.Do(ctx, 3, 200*time.Millisecond, func() error {
		return s.publisher.Publish(ctx, domain.RoutingDeleted, "delete:"+avatar.ID, body)
	}); err != nil {
		s.logger.ErrorContext(ctx, "publish delete event", slog.String("avatar_id", avatar.ID), slog.Any("error", err))
	}
	s.metrics.ObserveDelete("success", avatar.SizeBytes)
	s.logger.InfoContext(ctx, "avatar deleted", "avatar_id", avatar.ID, "user_id", avatar.UserID)
	return nil
}

// sanitizeFileName убирает путь из имени файла.
func sanitizeFileName(name string) string {
	base := filepath.Base(name)
	base = strings.ReplaceAll(base, "..", "")
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "avatar"
	}
	return base
}
