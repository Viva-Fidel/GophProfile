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

	"gophprofile/internal/broker"
	"gophprofile/internal/domain"
	"gophprofile/internal/imageutil"
	"gophprofile/internal/repository"
	"gophprofile/internal/storage"
	"gophprofile/pkg/retry"
)

// AvatarService оркестрирует валидацию, S3, БД и события брокера.
type AvatarService struct {
	repo      repository.AvatarRepository
	objects   storage.ObjectStorage
	publisher broker.Publisher
	publicURL string
}

// NewAvatarService создаёт сервис аватарок.
func NewAvatarService(repo repository.AvatarRepository, objects storage.ObjectStorage, publisher broker.Publisher, publicURL string) *AvatarService {
	return &AvatarService{
		repo:      repo,
		objects:   objects,
		publisher: publisher,
		publicURL: strings.TrimRight(publicURL, "/"),
	}
}

// Upload валидирует файл, сохраняет оригинал в S3, пишет метаданные и публикует событие.
func (s *AvatarService) Upload(ctx context.Context, userID, fileName string, data []byte) (*domain.Avatar, error) {
	if userID == "" {
		return nil, domain.ErrMissingUser
	}
	if len(data) == 0 {
		return nil, domain.ErrMissingFile
	}
	if int64(len(data)) > domain.MaxUploadBytes {
		return nil, domain.ErrTooLarge
	}

	mime, width, height, err := imageutil.Detect(data)
	if err != nil {
		return nil, err
	}

	id := uuid.NewString()
	now := time.Now().UTC()
	s3Key := fmt.Sprintf("originals/%s/%s/%s", userID, id, sanitizeFileName(fileName))

	if err := s.objects.Upload(ctx, s3Key, data, mime); err != nil {
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
		return nil, err
	}

	event := domain.AvatarUploadEvent{AvatarID: id, UserID: userID, S3Key: s3Key}
	body, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if err := retry.Do(ctx, 3, 200*time.Millisecond, func() error {
		return s.publisher.Publish(ctx, domain.RoutingUploaded, "upload:"+id, body)
	}); err != nil {
		slog.ErrorContext(ctx, "publish upload event", slog.String("avatar_id", id), slog.Any("error", err))
	}

	return &avatar, nil
}

// Get возвращает аватарку по id.
func (s *AvatarService) Get(ctx context.Context, id string) (*domain.Avatar, error) {
	return s.repo.GetByID(ctx, id)
}

// GetLatestByUser возвращает последнюю аватарку пользователя.
func (s *AvatarService) GetLatestByUser(ctx context.Context, userID string) (*domain.Avatar, error) {
	return s.repo.GetLatestByUserID(ctx, userID)
}

// ListByUser возвращает список аватарок пользователя.
func (s *AvatarService) ListByUser(ctx context.Context, userID string) ([]domain.Avatar, error) {
	items, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		return []domain.Avatar{}, nil
	}
	return items, nil
}

// File отдаёт байты изображения нужного размера и формата.
func (s *AvatarService) File(ctx context.Context, avatar *domain.Avatar, size, format string) (data []byte, contentType, etag string, err error) {
	_, _, original, err := imageutil.ParseSize(size)
	if err != nil {
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
		return nil, "", "", err
	}
	if contentType == "" {
		contentType = avatar.MimeType
	}

	if format != "" {
		converted, mime, convErr := imageutil.Convert(data, format)
		if convErr != nil {
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
	if actorUserID == "" {
		return domain.ErrMissingUser
	}
	avatar, err := s.repo.GetByID(ctx, avatarID)
	if err != nil {
		return err
	}
	if avatar.UserID != actorUserID {
		return domain.ErrForbidden
	}
	return s.softDeleteAndPublish(ctx, avatar)
}

// DeleteUserAvatar мягко удаляет текущую аватарку пользователя.
func (s *AvatarService) DeleteUserAvatar(ctx context.Context, actorUserID, userID string) error {
	if actorUserID == "" {
		return domain.ErrMissingUser
	}
	if actorUserID != userID {
		return domain.ErrForbidden
	}
	avatar, err := s.repo.GetLatestByUserID(ctx, userID)
	if err != nil {
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
		return err
	}
	if err := retry.Do(ctx, 3, 200*time.Millisecond, func() error {
		return s.publisher.Publish(ctx, domain.RoutingDeleted, "delete:"+avatar.ID, body)
	}); err != nil {
		slog.ErrorContext(ctx, "publish delete event", slog.String("avatar_id", avatar.ID), slog.Any("error", err))
	}
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
