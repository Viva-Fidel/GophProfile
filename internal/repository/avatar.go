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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"gophprofile/internal/domain"
)

const dbTracerName = "gophprofile/repository"

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

func startDBSpan(ctx context.Context, op, statement string) (context.Context, trace.Span) {
	return otel.Tracer(dbTracerName).Start(ctx, "db."+op,
		trace.WithAttributes(
			attribute.String("db.system", "postgresql"),
			attribute.String("db.operation", op),
			attribute.String("db.statement", statement),
		),
	)
}

// Create сохраняет новую запись аватарки.
func (r *PostgresAvatarRepository) Create(ctx context.Context, a domain.Avatar) error {
	ctx, span := startDBSpan(ctx, "insert", "INSERT INTO avatars")
	defer span.End()

	thumbs, err := marshalThumbs(a.ThumbnailS3Keys)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO avatars (
			id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, a.ID, a.UserID, a.FileName, a.MimeType, a.SizeBytes, a.Width, a.Height,
		a.S3Key, thumbs, a.UploadStatus, a.ProcessingStatus, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// GetByID возвращает незакрытую аватарку по id.
func (r *PostgresAvatarRepository) GetByID(ctx context.Context, id string) (*domain.Avatar, error) {
	ctx, span := startDBSpan(ctx, "select", "SELECT FROM avatars BY id")
	defer span.End()
	span.SetAttributes(attribute.String("avatar.id", id))

	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
		FROM avatars WHERE id = $1 AND deleted_at IS NULL
	`, id)
	a, err := scanAvatar(row)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return a, err
}

// GetLatestByUserID возвращает последнюю аватарку пользователя.
func (r *PostgresAvatarRepository) GetLatestByUserID(ctx context.Context, userID string) (*domain.Avatar, error) {
	ctx, span := startDBSpan(ctx, "select", "SELECT FROM avatars BY user_id LIMIT 1")
	defer span.End()
	span.SetAttributes(attribute.String("user.id", userID))

	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
		FROM avatars WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, userID)
	a, err := scanAvatar(row)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return a, err
}

// ListByUserID возвращает все незакрытые аватарки пользователя.
func (r *PostgresAvatarRepository) ListByUserID(ctx context.Context, userID string) ([]domain.Avatar, error) {
	ctx, span := startDBSpan(ctx, "select", "SELECT FROM avatars BY user_id")
	defer span.End()
	span.SetAttributes(attribute.String("user.id", userID))

	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
		FROM avatars WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	defer rows.Close()

	var result []domain.Avatar
	for rows.Next() {
		a, err := scanAvatar(rows)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		result = append(result, *a)
	}
	return result, rows.Err()
}

// SoftDelete помечает аватарку удалённой.
func (r *PostgresAvatarRepository) SoftDelete(ctx context.Context, id string) error {
	ctx, span := startDBSpan(ctx, "update", "UPDATE avatars soft delete")
	defer span.End()
	span.SetAttributes(attribute.String("avatar.id", id))

	tag, err := r.db.Exec(ctx, `
		UPDATE avatars SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
	ctx, span := startDBSpan(ctx, "update", "UPDATE avatars claim processing")
	defer span.End()
	span.SetAttributes(attribute.String("avatar.id", id))

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
			if !errors.Is(getErr, domain.ErrNotFound) {
				span.RecordError(getErr)
				span.SetStatus(codes.Error, getErr.Error())
			}
			return nil, getErr
		}
		if existing.ProcessingStatus == domain.ProcessingCompleted {
			return existing, nil
		}
		return nil, domain.ErrNotFound
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return a, err
}

// GetByIDIncludingDeleted возвращает аватарку без фильтра по deleted_at.
func (r *PostgresAvatarRepository) GetByIDIncludingDeleted(ctx context.Context, id string) (*domain.Avatar, error) {
	ctx, span := startDBSpan(ctx, "select", "SELECT FROM avatars BY id including deleted")
	defer span.End()

	row := r.db.QueryRow(ctx, `
		SELECT id, user_id, file_name, mime_type, size_bytes, width, height,
			s3_key, thumbnail_s3_keys, upload_status, processing_status,
			created_at, updated_at, deleted_at
		FROM avatars WHERE id = $1
	`, id)
	a, err := scanAvatar(row)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return a, err
}

// UpdateProcessingResult сохраняет ключи миниатюр и статус обработки.
func (r *PostgresAvatarRepository) UpdateProcessingResult(ctx context.Context, id string, thumbs map[string]string, status string) error {
	ctx, span := startDBSpan(ctx, "update", "UPDATE avatars processing result")
	defer span.End()
	span.SetAttributes(
		attribute.String("avatar.id", id),
		attribute.String("processing.status", status),
	)

	payload, err := marshalThumbs(thumbs)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE avatars
		SET thumbnail_s3_keys = $2, processing_status = $3, updated_at = NOW()
		WHERE id = $1
	`, id, payload, status)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// MarkProcessingFailed помечает обработку как неуспешную.
func (r *PostgresAvatarRepository) MarkProcessingFailed(ctx context.Context, id string) error {
	ctx, span := startDBSpan(ctx, "update", "UPDATE avatars processing failed")
	defer span.End()

	_, err := r.db.Exec(ctx, `
		UPDATE avatars SET processing_status = $2, updated_at = NOW() WHERE id = $1
	`, id, domain.ProcessingFailed)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
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
