// Package domain содержит доменные сущности, статусы и ошибки аватарок.
package domain

import "time"

const (
	// UploadStatusUploading — файл ещё загружается в S3.
	UploadStatusUploading = "uploading"
	// UploadStatusUploaded — оригинал сохранён в S3.
	UploadStatusUploaded = "uploaded"
	// UploadStatusFailed — загрузка в S3 не удалась.
	UploadStatusFailed = "failed"

	// ProcessingPending — миниатюры ещё не созданы.
	ProcessingPending = "pending"
	// ProcessingProcessing — воркер обрабатывает изображение.
	ProcessingProcessing = "processing"
	// ProcessingCompleted — миниатюры готовы.
	ProcessingCompleted = "completed"
	// ProcessingFailed — обработка изображения не удалась.
	ProcessingFailed = "failed"

	// StatusProcessing — публичный статус «в обработке».
	StatusProcessing = "processing"
	// StatusReady — публичный статус «готово».
	StatusReady = "ready"
	// StatusFailed — публичный статус «ошибка».
	StatusFailed = "failed"

	// SizeOriginal — оригинальный размер изображения.
	SizeOriginal = "original"
	// Size100 — миниатюра 100x100.
	Size100 = "100x100"
	// Size300 — миниатюра 300x300.
	Size300 = "300x300"

	// MaxUploadBytes — максимальный размер загружаемого файла (10 MiB).
	MaxUploadBytes int64 = 10 << 20
)

// Avatar — метаданные аватарки пользователя.
type Avatar struct {
	ID               string
	UserID           string
	FileName         string
	MimeType         string
	SizeBytes        int64
	Width            int
	Height           int
	S3Key            string
	ThumbnailS3Keys  map[string]string
	UploadStatus     string
	ProcessingStatus string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

// PublicStatus возвращает статус аватарки для API-ответа.
func (a Avatar) PublicStatus() string {
	switch a.ProcessingStatus {
	case ProcessingCompleted:
		return StatusReady
	case ProcessingFailed:
		return StatusFailed
	default:
		return StatusProcessing
	}
}

// ThumbnailKey возвращает S3-ключ миниатюры указанного размера.
func (a Avatar) ThumbnailKey(size string) (string, bool) {
	if a.ThumbnailS3Keys == nil {
		return "", false
	}
	key, ok := a.ThumbnailS3Keys[size]
	return key, ok && key != ""
}
