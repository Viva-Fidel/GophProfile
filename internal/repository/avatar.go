// Package repository хранит метаданные аватарок в PostgreSQL.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"gophprofile/internal/domain"
)

type pgPool interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PostgresAvatarRepository хранит метаданные аватарок в PostgreSQL.
type PostgresAvatarRepository struct {
	db pgPool
}

// NewPostgresAvatarRepository создаёт репозиторий аватарок.
func NewPostgresAvatarRepository(db *pgxpool.Pool) *PostgresAvatarRepository {
	return &PostgresAvatarRepository{db: db}
}

// Create сохраняет новую запись аватарки.
func (r *PostgresAvatarRepository) Create(ctx context.Context, a domain.Avatar) error {
	thumbs, err := marshalThumbs(a.ThumbnailS3Keys)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO avatars (
			id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, a.ID, a.UserID, a.FileName, a.MimeType, a.SizeBytes, a.Width, a.Height,
		a.S3Key, thumbs, a.UploadStatus, a.ProcessingStatus, a.CreatedAt, a.UpdatedAt)
	return err
}

// GetByID возвращает незакрытую аватарку по id.
func (r *PostgresAvatarRepository) GetByID(ctx context.Context, id string) (*domain.Avatar, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
		FROM avatars WHERE id = $1 AND deleted_at IS NULL
	`, id)
	return scanAvatar(row)
}

// GetLatestByUserID возвращает последнюю аватарку пользователя.
func (r *PostgresAvatarRepository) GetLatestByUserID(ctx context.Context, userID string) (*domain.Avatar, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
		FROM avatars WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, userID)
	return scanAvatar(row)
}

// ListByUserID возвращает все незакрытые аватарки пользователя.
func (r *PostgresAvatarRepository) ListByUserID(ctx context.Context, userID string) ([]domain.Avatar, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
		FROM avatars WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.Avatar
	for rows.Next() {
		a, err := scanAvatar(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *a)
	}
	return result, rows.Err()
}

// SoftDelete помечает аватарку удалённой.
func (r *PostgresAvatarRepository) SoftDelete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE avatars SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ClaimForProcessing атомарно берёт аватарку в обработку.
// Если обработка уже завершена, возвращает существующую запись без ошибки.
func (r *PostgresAvatarRepository) ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE avatars
		SET processing_status = $2, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL AND processing_status IN ($3, $4, $5)
		RETURNING id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
	`, id, domain.ProcessingProcessing, domain.ProcessingPending, domain.ProcessingFailed, domain.ProcessingProcessing)
	a, err := scanAvatar(row)
	if errors.Is(err, domain.ErrNotFound) {
		existing, getErr := r.GetByIDIncludingDeleted(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		if existing.ProcessingStatus == domain.ProcessingCompleted {
			return existing, nil
		}
		return nil, domain.ErrNotFound
	}
	return a, err
}

// GetByIDIncludingDeleted возвращает аватарку без фильтра по deleted_at.
func (r *PostgresAvatarRepository) GetByIDIncludingDeleted(ctx context.Context, id string) (*domain.Avatar, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
		FROM avatars WHERE id = $1
	`, id)
	return scanAvatar(row)
}

// UpdateProcessingResult сохраняет ключи миниатюр и статус обработки.
func (r *PostgresAvatarRepository) UpdateProcessingResult(ctx context.Context, id string, thumbs map[string]string, status string) error {
	payload, err := marshalThumbs(thumbs)
	if err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE avatars
		SET thumbnail_s3_keys = $2, processing_status = $3, updated_at = NOW()
		WHERE id = $1
	`, id, payload, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// MarkProcessingFailed помечает обработку как неуспешную.
func (r *PostgresAvatarRepository) MarkProcessingFailed(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE avatars SET processing_status = $2, updated_at = NOW() WHERE id = $1
	`, id, domain.ProcessingFailed)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

// scanAvatar читает строку выборки в Avatar.
func scanAvatar(row rowScanner) (*domain.Avatar, error) {
	var a domain.Avatar
	var thumbs []byte
	var deletedAt *time.Time
	err := row.Scan(
		&a.ID, &a.UserID, &a.FileName, &a.MimeType, &a.SizeBytes, &a.Width, &a.Height,
		&a.S3Key, &thumbs, &a.UploadStatus, &a.ProcessingStatus,
		&a.CreatedAt, &a.UpdatedAt, &deletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.ThumbnailS3Keys, err = unmarshalThumbs(thumbs)
	if err != nil {
		return nil, err
	}
	a.DeletedAt = deletedAt
	return &a, nil
}

// marshalThumbs сериализует карту ключей миниатюр в JSON.
func marshalThumbs(m map[string]string) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

// unmarshalThumbs десериализует JSONB с ключами миниатюр.
func unmarshalThumbs(data []byte) (map[string]string, error) {
	if len(data) == 0 {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
