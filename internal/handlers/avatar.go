// Package handlers реализует HTTP-обработчики REST API.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gophprofile/internal/domain"
	"gophprofile/internal/imageutil"
)

// AvatarService — контракт сервиса аватарок для HTTP-обработчика.
type AvatarService interface {
	Upload(ctx context.Context, userID, fileName string, data []byte) (*domain.Avatar, error)
	Get(ctx context.Context, id string) (*domain.Avatar, error)
	GetLatestByUser(ctx context.Context, userID string) (*domain.Avatar, error)
	ListByUser(ctx context.Context, userID string) ([]domain.Avatar, error)
	File(ctx context.Context, avatar *domain.Avatar, size, format string) (data []byte, contentType, etag string, err error)
	Delete(ctx context.Context, actorUserID, avatarID string) error
	DeleteUserAvatar(ctx context.Context, actorUserID, userID string) error
	AvatarURL(id string) string
	ThumbnailURL(id, size string) string
}

// AvatarHandler обрабатывает HTTP-запросы аватарок.
type AvatarHandler struct {
	svc    AvatarService
	logger *slog.Logger
}

// NewAvatarHandler создаёт HTTP-обработчик аватарок.
func NewAvatarHandler(svc AvatarService, logger *slog.Logger) *AvatarHandler {
	return &AvatarHandler{svc: svc, logger: logger}
}

// Upload принимает multipart-файл и создаёт аватарку.
func (h *AvatarHandler) Upload(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("Missing user id", "X-User-ID header is required"))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxUploadBytes+1024)
	if err := r.ParseMultipartForm(domain.MaxUploadBytes); err != nil {
		writeUploadParseError(w, err)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		file, hdr, err = r.FormFile("image")
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("Missing file", "form field file or image is required"))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, domain.MaxUploadBytes+1))
	if err != nil {
		writeUploadParseError(w, err)
		return
	}
	if int64(len(data)) > domain.MaxUploadBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, tooLargeBody())
		return
	}

	avatar, err := h.svc.Upload(r.Context(), userID, hdr.Filename, data)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toUploadResponse(avatar, h.svc.AvatarURL(avatar.ID)))
}

// Get отдаёт бинарные данные аватарки.
func (h *AvatarHandler) Get(w http.ResponseWriter, r *http.Request) {
	h.serveFile(w, r, chi.URLParam(r, "avatar_id"))
}

// GetUserAvatar отдаёт последнюю аватарку пользователя.
func (h *AvatarHandler) GetUserAvatar(w http.ResponseWriter, r *http.Request) {
	avatar, err := h.svc.GetLatestByUser(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeFile(w, r, avatar)
}

// GetMetadata отдаёт метаданные аватарки.
func (h *AvatarHandler) GetMetadata(w http.ResponseWriter, r *http.Request) {
	avatar, err := h.svc.Get(r.Context(), chi.URLParam(r, "avatar_id"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toMetadata(avatar, h.svc))
}

// ListUserAvatars отдаёт список аватарок пользователя.
func (h *AvatarHandler) ListUserAvatars(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListByUser(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	out := make([]MetadataResponse, 0, len(items))
	for i := range items {
		out = append(out, toMetadata(&items[i], h.svc))
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete мягко удаляет аватарку по id.
func (h *AvatarHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
	if err := h.svc.Delete(r.Context(), userID, chi.URLParam(r, "avatar_id")); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteUserAvatar мягко удаляет текущую аватарку пользователя.
func (h *AvatarHandler) DeleteUserAvatar(w http.ResponseWriter, r *http.Request) {
	actor := strings.TrimSpace(r.Header.Get("X-User-ID"))
	if err := h.svc.DeleteUserAvatar(r.Context(), actor, chi.URLParam(r, "user_id")); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveFile загружает метаданные и отдаёт файл.
func (h *AvatarHandler) serveFile(w http.ResponseWriter, r *http.Request, id string) {
	avatar, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeFile(w, r, avatar)
}

// writeFile пишет бинарный ответ с Cache-Control и ETag.
func (h *AvatarHandler) writeFile(w http.ResponseWriter, r *http.Request, avatar *domain.Avatar) {
	size := r.URL.Query().Get("size")
	format := r.URL.Query().Get("format")
	if _, _, _, err := imageutil.ParseSize(size); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("Invalid size", `Supported sizes: "100x100", "300x300", "original"`))
		return
	}
	data, contentType, etag, err := h.svc.File(r.Context(), avatar, size, format)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "max-age=86400")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// writeServiceError пишет HTTP-статус по типу доменной ошибки.
func (h *AvatarHandler) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody("Avatar not found", ""))
	case errors.Is(err, domain.ErrForbidden):
		writeJSON(w, http.StatusForbidden, errorBody("Forbidden", "You can only delete your own avatars"))
	case errors.Is(err, domain.ErrInvalidFormat):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "Invalid file format",
			"details": "Supported formats: jpeg, png, webp",
		})
	case errors.Is(err, domain.ErrTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, tooLargeBody())
	case errors.Is(err, domain.ErrMissingFile), errors.Is(err, domain.ErrMissingUser):
		writeJSON(w, http.StatusBadRequest, errorBody(err.Error(), ""))
	case errors.Is(err, domain.ErrInvalidSize):
		writeJSON(w, http.StatusBadRequest, errorBody("Invalid size", `Supported sizes: "100x100", "300x300", "original"`))
	case errors.Is(err, domain.ErrInvalidFormatParam):
		writeJSON(w, http.StatusBadRequest, errorBody("Invalid format", `Supported formats: jpeg, png`))
	default:
		h.logger.ErrorContext(r.Context(), "avatar handler", slog.Any("error", err))
		writeJSON(w, http.StatusInternalServerError, errorBody("Internal server error", ""))
	}
}

// writeUploadParseError пишет статус по ошибке чтения multipart.
func writeUploadParseError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large") {
		writeJSON(w, http.StatusRequestEntityTooLarge, tooLargeBody())
		return
	}
	writeJSON(w, http.StatusBadRequest, errorBody("Invalid multipart form", err.Error()))
}

// ErrorResponse — JSON-ошибка API.
type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
	MaxSize int64  `json:"max_size,omitempty"`
}

// UploadResponse — ответ на успешную загрузку.
type UploadResponse struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	URL       string    `json:"url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Dimensions — ширина и высота изображения.
type Dimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ThumbnailDTO — ссылка на миниатюру.
type ThumbnailDTO struct {
	Size string `json:"size"`
	URL  string `json:"url"`
}

// MetadataResponse — метаданные аватарки в API.
type MetadataResponse struct {
	ID         string         `json:"id"`
	UserID     string         `json:"user_id"`
	FileName   string         `json:"file_name"`
	MimeType   string         `json:"mime_type"`
	Size       int64          `json:"size"`
	Dimensions Dimensions     `json:"dimensions"`
	Thumbnails []ThumbnailDTO `json:"thumbnails"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// toUploadResponse преобразует Avatar в ответ загрузки.
func toUploadResponse(a *domain.Avatar, url string) UploadResponse {
	return UploadResponse{
		ID:        a.ID,
		UserID:    a.UserID,
		URL:       url,
		Status:    a.PublicStatus(),
		CreatedAt: a.CreatedAt,
	}
}

// toMetadata преобразует Avatar в ответ метаданных.
func toMetadata(a *domain.Avatar, svc AvatarService) MetadataResponse {
	thumbs := make([]ThumbnailDTO, 0, len(a.ThumbnailS3Keys))
	for _, size := range []string{domain.Size100, domain.Size300} {
		if _, ok := a.ThumbnailKey(size); ok {
			thumbs = append(thumbs, ThumbnailDTO{Size: size, URL: svc.ThumbnailURL(a.ID, size)})
		}
	}
	return MetadataResponse{
		ID:       a.ID,
		UserID:   a.UserID,
		FileName: a.FileName,
		MimeType: a.MimeType,
		Size:     a.SizeBytes,
		Dimensions: Dimensions{
			Width:  a.Width,
			Height: a.Height,
		},
		Thumbnails: thumbs,
		CreatedAt:  a.CreatedAt,
		UpdatedAt:  a.UpdatedAt,
	}
}

// tooLargeBody формирует JSON для ошибки 413.
func tooLargeBody() map[string]any {
	return map[string]any{
		"error":    "File too large",
		"max_size": domain.MaxUploadBytes,
	}
}

// errorBody формирует JSON-ошибку.
func errorBody(msg, details string) ErrorResponse {
	return ErrorResponse{Error: msg, Details: details}
}

// writeJSON отдаёт JSON-ответ с указанным статусом.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
