package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"gophprofile/internal/domain"
	"gophprofile/internal/services"
)

type memRepo struct {
	items map[string]domain.Avatar
}

func (m *memRepo) Create(_ context.Context, a domain.Avatar) error {
	m.items[a.ID] = a
	return nil
}
func (m *memRepo) GetByID(_ context.Context, id string) (*domain.Avatar, error) {
	a, ok := m.items[id]
	if !ok || a.DeletedAt != nil {
		return nil, domain.ErrNotFound
	}
	out := a
	return &out, nil
}
func (m *memRepo) GetLatestByUserID(_ context.Context, userID string) (*domain.Avatar, error) {
	for _, a := range m.items {
		if a.UserID == userID && a.DeletedAt == nil {
			out := a
			return &out, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (m *memRepo) ListByUserID(_ context.Context, userID string) ([]domain.Avatar, error) {
	var out []domain.Avatar
	for _, a := range m.items {
		if a.UserID == userID && a.DeletedAt == nil {
			out = append(out, a)
		}
	}
	return out, nil
}
func (m *memRepo) SoftDelete(_ context.Context, id string) error {
	a, ok := m.items[id]
	if !ok || a.DeletedAt != nil {
		return domain.ErrNotFound
	}
	now := time.Now()
	a.DeletedAt = &now
	m.items[id] = a
	return nil
}
func (m *memRepo) ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error) {
	return m.GetByID(ctx, id)
}
func (m *memRepo) UpdateProcessingResult(_ context.Context, id string, thumbs map[string]string, status string) error {
	a := m.items[id]
	a.ThumbnailS3Keys = thumbs
	a.ProcessingStatus = status
	m.items[id] = a
	return nil
}
func (m *memRepo) MarkProcessingFailed(_ context.Context, id string) error { return nil }

type memStore struct{ objects map[string][]byte }

func (s *memStore) Upload(_ context.Context, key string, data []byte, _ string) error {
	s.objects[key] = append([]byte(nil), data...)
	return nil
}
func (s *memStore) Download(_ context.Context, key string) ([]byte, string, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, "", errors.New("missing")
	}
	return data, "image/png", nil
}
func (s *memStore) Delete(_ context.Context, keys []string) error { return nil }
func (s *memStore) Ping(context.Context) error                    { return nil }

type memPub struct{}

func (memPub) Publish(context.Context, string, string, []byte) error { return nil }
func (memPub) Ping(context.Context) error                            { return nil }
func (memPub) Close() error                                          { return nil }

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newMux(t *testing.T) (*chi.Mux, *services.AvatarService) {
	t.Helper()
	svc := services.NewAvatarService(&memRepo{items: map[string]domain.Avatar{}}, &memStore{objects: map[string][]byte{}}, memPub{}, "http://localhost:8080")
	h := NewAvatarHandler(svc)
	r := chi.NewRouter()
	r.Post("/api/v1/avatars", h.Upload)
	r.Get("/api/v1/avatars/{avatar_id}", h.Get)
	r.Get("/api/v1/avatars/{avatar_id}/metadata", h.GetMetadata)
	r.Delete("/api/v1/avatars/{avatar_id}", h.Delete)
	r.Get("/api/v1/users/{user_id}/avatar", h.GetUserAvatar)
	r.Get("/api/v1/users/{user_id}/avatars", h.ListUserAvatars)
	r.Delete("/api/v1/users/{user_id}/avatar", h.DeleteUserAvatar)
	return r, svc
}

func multipartPNG(t *testing.T, field, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestHandlerUploadAndGet(t *testing.T) {
	mux, _ := newMux(t)
	body, ctype := multipartPNG(t, "file", "a.png", pngBytes(t))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("X-User-ID", "user-1")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatal(rr.Code, rr.Body.String())
	}
	var created UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != domain.StatusProcessing || created.UserID != "user-1" {
		t.Fatal(created)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+created.ID+"/metadata", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+created.ID, nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("ETag") == "" {
		t.Fatal(rr.Code, rr.Header())
	}
	etag := rr.Header().Get("ETag")

	req = httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+created.ID, nil)
	req.Header.Set("If-None-Match", etag)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatal(rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/avatars", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/users/user-1/avatar", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+created.ID, nil)
	req.Header.Set("X-User-ID", "other")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatal(rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/avatars/"+created.ID, nil)
	req.Header.Set("X-User-ID", "user-1")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestHandlerUploadValidation(t *testing.T) {
	mux, _ := newMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatal(rr.Code)
	}

	body, ctype := multipartPNG(t, "file", "a.png", []byte("not-image"))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("X-User-ID", "u")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatal(rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/avatars/missing", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatal(rr.Code)
	}
}

func TestHandlerDeleteUserAvatar(t *testing.T) {
	mux, _ := newMux(t)
	body, ctype := multipartPNG(t, "file", "a.png", pngBytes(t))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("X-User-ID", "u1")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatal(rr.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/users/u1/avatar", nil)
	req.Header.Set("X-User-ID", "u1")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestHandlerUploadImageField(t *testing.T) {
	mux, _ := newMux(t)
	body, ctype := multipartPNG(t, "image", "a.png", pngBytes(t))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("X-User-ID", "user-1")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatal(rr.Code, rr.Body.String())
	}
}

func TestHandlerGetInvalidSize(t *testing.T) {
	mux, svc := newMux(t)
	av, err := svc.Upload(context.Background(), "u1", "a.png", pngBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/avatars/"+av.ID+"?size=1x1", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatal(rr.Code)
	}
}

func TestWriteJSONAndTooLarge(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusRequestEntityTooLarge, tooLargeBody())
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatal(rr.Code)
	}
	b, _ := io.ReadAll(rr.Body)
	if !bytes.Contains(b, []byte("File too large")) {
		t.Fatal(string(b))
	}
}
