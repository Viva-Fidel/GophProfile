package httpserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"gophprofile/internal/domain"
	"gophprofile/internal/handlers"
	"gophprofile/internal/observability"
	"gophprofile/internal/services"
)

type memRepo struct{ items map[string]domain.Avatar }

func (m *memRepo) Create(_ context.Context, a domain.Avatar) error { m.items[a.ID] = a; return nil }
func (m *memRepo) GetByID(_ context.Context, id string) (*domain.Avatar, error) {
	a, ok := m.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := a
	return &out, nil
}
func (m *memRepo) GetLatestByUserID(context.Context, string) (*domain.Avatar, error) {
	return nil, domain.ErrNotFound
}
func (m *memRepo) ListByUserID(context.Context, string) ([]domain.Avatar, error) {
	return []domain.Avatar{}, nil
}
func (m *memRepo) SoftDelete(context.Context, string) error { return nil }
func (m *memRepo) ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error) {
	return m.GetByID(ctx, id)
}
func (m *memRepo) UpdateProcessingResult(context.Context, string, map[string]string, string) error {
	return nil
}
func (m *memRepo) MarkProcessingFailed(context.Context, string) error { return nil }

type memStore struct{ objects map[string][]byte }

func (s *memStore) Upload(_ context.Context, key string, data []byte, _ string) error {
	s.objects[key] = append([]byte(nil), data...)
	return nil
}
func (s *memStore) Download(_ context.Context, key string) ([]byte, string, error) {
	return s.objects[key], "image/png", nil
}
func (s *memStore) Delete(context.Context, []string) error { return nil }
func (s *memStore) Ping(context.Context) error             { return nil }

type memPub struct{}

func (memPub) Publish(context.Context, string, string, []byte) error { return nil }
func (memPub) Ping(context.Context) error                            { return nil }
func (memPub) Close() error                                          { return nil }

type pingOK struct{}

func (pingOK) Ping(context.Context) error { return nil }

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{4, 5, 6, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRouter(t *testing.T) {
	webDir := filepath.Join("..", "..", "web")
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	svc := services.NewAvatarService(&memRepo{items: map[string]domain.Avatar{}}, &memStore{objects: map[string][]byte{}}, memPub{}, "http://localhost:8080", slog.New(slog.DiscardHandler), metrics)
	h := New(
		handlers.NewAvatarHandler(svc, slog.New(slog.DiscardHandler)),
		handlers.NewHealthHandler(services.NewHealthService(nil, pingOK{}, pingOK{})),
		webDir,
		slog.New(slog.DiscardHandler),
		metrics,
	).Router()

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rr.Code != http.StatusServiceUnavailable && rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK || !bytes.Contains(rr.Body.Bytes(), []byte("Avatar Upload Service")) {
		t.Fatal(rr.Code, rr.Body.String())
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "a.png")
	_, _ = fw.Write(pngBytes(t))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/avatars", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-User-ID", "u1")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatal(rr.Code, rr.Body.String())
	}
}
