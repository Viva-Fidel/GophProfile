package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"gophprofile/internal/domain"
)

func columns() []string {
	return []string{
		"id", "user_id", "file_name", "mime_type", "size_bytes", "width", "height",
		"s3_key", "thumbnail_s3_keys", "upload_status", "processing_status",
		"created_at", "updated_at", "deleted_at",
	}
}

func TestAvatarRepositoryCRUD(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewPostgresAvatarRepository(db)
	now := time.Now()
	thumbs := []byte(`{"100x100":"t1"}`)

	mock.ExpectExec(`INSERT INTO avatars`).
		WithArgs("id1", "u1", "a.png", "image/png", int64(10), 8, 8, "s3", []byte("{}"), domain.UploadStatusUploaded, domain.ProcessingPending, now, now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err = repo.Create(context.Background(), domain.Avatar{
		ID: "id1", UserID: "u1", FileName: "a.png", MimeType: "image/png", SizeBytes: 10,
		Width: 8, Height: 8, S3Key: "s3", ThumbnailS3Keys: map[string]string{},
		UploadStatus: domain.UploadStatusUploaded, ProcessingStatus: domain.ProcessingPending,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(`SELECT id, user_id, file_name`).
		WithArgs("id1").
		WillReturnRows(sqlmock.NewRows(columns()).AddRow(
			"id1", "u1", "a.png", "image/png", int64(10), 8, 8, "s3", thumbs,
			domain.UploadStatusUploaded, domain.ProcessingPending, now, now, nil,
		))
	got, err := repo.GetByID(context.Background(), "id1")
	if err != nil || got.FileName != "a.png" || got.ThumbnailS3Keys["100x100"] != "t1" {
		t.Fatal(err, got)
	}

	mock.ExpectQuery(`SELECT id, user_id, file_name`).
		WithArgs("u1").
		WillReturnRows(sqlmock.NewRows(columns()).AddRow(
			"id1", "u1", "a.png", "image/png", int64(10), 8, 8, "s3", thumbs,
			domain.UploadStatusUploaded, domain.ProcessingPending, now, now, nil,
		))
	latest, err := repo.GetLatestByUserID(context.Background(), "u1")
	if err != nil || latest.ID != "id1" {
		t.Fatal(err, latest)
	}

	mock.ExpectQuery(`SELECT id, user_id, file_name`).
		WithArgs("u1").
		WillReturnRows(sqlmock.NewRows(columns()).AddRow(
			"id1", "u1", "a.png", "image/png", int64(10), 8, 8, "s3", thumbs,
			domain.UploadStatusUploaded, domain.ProcessingPending, now, now, nil,
		))
	list, err := repo.ListByUserID(context.Background(), "u1")
	if err != nil || len(list) != 1 {
		t.Fatal(err, list)
	}

	mock.ExpectExec(`UPDATE avatars SET deleted_at`).
		WithArgs("id1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.SoftDelete(context.Background(), "id1"); err != nil {
		t.Fatal(err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAvatarRepositoryNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewPostgresAvatarRepository(db)

	mock.ExpectQuery(`SELECT id, user_id, file_name`).
		WithArgs("missing").
		WillReturnRows(sqlmock.NewRows(columns()))
	if _, err := repo.GetByID(context.Background(), "missing"); err != domain.ErrNotFound {
		t.Fatal(err)
	}

	mock.ExpectExec(`UPDATE avatars SET deleted_at`).
		WithArgs("missing").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.SoftDelete(context.Background(), "missing"); err != domain.ErrNotFound {
		t.Fatal(err)
	}
}

func TestClaimAndUpdateProcessing(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewPostgresAvatarRepository(db)
	now := time.Now()

	mock.ExpectQuery(`UPDATE avatars`).
		WithArgs("id1", domain.ProcessingProcessing, domain.ProcessingPending, domain.ProcessingFailed, domain.ProcessingProcessing).
		WillReturnRows(sqlmock.NewRows(columns()).AddRow(
			"id1", "u1", "a.png", "image/png", int64(10), 8, 8, "s3", []byte("{}"),
			domain.UploadStatusUploaded, domain.ProcessingProcessing, now, now, nil,
		))
	got, err := repo.ClaimForProcessing(context.Background(), "id1")
	if err != nil || got.ProcessingStatus != domain.ProcessingProcessing {
		t.Fatal(err, got)
	}

	mock.ExpectExec(`UPDATE avatars`).
		WithArgs("id1", []byte(`{"100x100":"t"}`), domain.ProcessingCompleted).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.UpdateProcessingResult(context.Background(), "id1", map[string]string{"100x100": "t"}, domain.ProcessingCompleted); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec(`UPDATE avatars SET processing_status`).
		WithArgs("id1", domain.ProcessingFailed).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.MarkProcessingFailed(context.Background(), "id1"); err != nil {
		t.Fatal(err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnmarshalThumbs(t *testing.T) {
	m, err := unmarshalThumbs(nil)
	if err != nil || len(m) != 0 {
		t.Fatal(err, m)
	}
	m, err = unmarshalThumbs([]byte(`{"a":"b"}`))
	if err != nil || m["a"] != "b" {
		t.Fatal(err, m)
	}
}
