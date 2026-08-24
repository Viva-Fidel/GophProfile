package worker

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"gophprofile/internal/broker"
	"gophprofile/internal/domain"
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
	if !ok {
		return nil, domain.ErrNotFound
	}
	out := a
	return &out, nil
}
func (m *memRepo) GetLatestByUserID(context.Context, string) (*domain.Avatar, error) {
	return nil, domain.ErrNotFound
}
func (m *memRepo) ListByUserID(context.Context, string) ([]domain.Avatar, error) { return nil, nil }
func (m *memRepo) SoftDelete(context.Context, string) error                      { return nil }
func (m *memRepo) ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error) {
	a, err := m.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	a.ProcessingStatus = domain.ProcessingProcessing
	m.items[id] = *a
	return a, nil
}
func (m *memRepo) UpdateProcessingResult(_ context.Context, id string, thumbs map[string]string, status string) error {
	a := m.items[id]
	a.ThumbnailS3Keys = thumbs
	a.ProcessingStatus = status
	m.items[id] = a
	return nil
}
func (m *memRepo) MarkProcessingFailed(_ context.Context, id string) error {
	a := m.items[id]
	a.ProcessingStatus = domain.ProcessingFailed
	m.items[id] = a
	return nil
}

type memStore struct {
	objects map[string][]byte
}

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
func (s *memStore) Delete(_ context.Context, keys []string) error {
	for _, k := range keys {
		delete(s.objects, k)
	}
	return nil
}
func (s *memStore) Ping(context.Context) error { return nil }

func pngBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	img.Set(0, 0, color.RGBA{9, 8, 7, 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestHandleUploadEvent(t *testing.T) {
	data := pngBytes()
	repo := &memRepo{items: map[string]domain.Avatar{
		"id1": {
			ID: "id1", UserID: "u1", S3Key: "orig", ProcessingStatus: domain.ProcessingPending,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		},
	}}
	store := &memStore{objects: map[string][]byte{"orig": data}}
	w := New(repo, store, nil)
	if err := w.HandleUploadEvent(context.Background(), domain.AvatarUploadEvent{AvatarID: "id1", S3Key: "orig"}); err != nil {
		t.Fatal(err)
	}
	got := repo.items["id1"]
	if got.ProcessingStatus != domain.ProcessingCompleted {
		t.Fatal(got.ProcessingStatus)
	}
	if _, ok := got.ThumbnailS3Keys[domain.Size100]; !ok {
		t.Fatal(got.ThumbnailS3Keys)
	}
	if _, ok := store.objects["thumbnails/id1/100x100.jpg"]; !ok {
		t.Fatal("missing thumb")
	}
}

func TestHandleUploadEventIdempotent(t *testing.T) {
	repo := &memRepo{items: map[string]domain.Avatar{
		"id1": {ID: "id1", ProcessingStatus: domain.ProcessingCompleted},
	}}
	w := New(repo, &memStore{objects: map[string][]byte{}}, nil)
	if err := w.HandleUploadEvent(context.Background(), domain.AvatarUploadEvent{AvatarID: "id1"}); err != nil {
		t.Fatal(err)
	}
}

func TestHandleUploadEventMissing(t *testing.T) {
	w := New(&memRepo{items: map[string]domain.Avatar{}}, &memStore{objects: map[string][]byte{}}, nil)
	if err := w.HandleUploadEvent(context.Background(), domain.AvatarUploadEvent{AvatarID: "no"}); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDeleteEvent(t *testing.T) {
	store := &memStore{objects: map[string][]byte{"a": []byte("1"), "b": []byte("2")}}
	w := New(&memRepo{items: map[string]domain.Avatar{}}, store, nil)
	if err := w.HandleDeleteEvent(context.Background(), domain.AvatarDeleteEvent{AvatarID: "id", S3Keys: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if len(store.objects) != 0 {
		t.Fatal(store.objects)
	}
}

type stubConsumer struct{}

func (stubConsumer) Consume(context.Context, string, func(broker.Message) error) error {
	return errors.New("stop")
}

func TestHandleUploadedInvalidJSON(t *testing.T) {
	w := New(&memRepo{items: map[string]domain.Avatar{}}, &memStore{objects: map[string][]byte{}}, nil)
	if err := w.handleUploaded(broker.Message{Body: []byte("not-json")}); err != nil {
		t.Fatal(err)
	}
	if err := w.handleDeleted(broker.Message{Body: []byte("not-json")}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRun(t *testing.T) {
	w := New(&memRepo{items: map[string]domain.Avatar{}}, &memStore{objects: map[string][]byte{}}, stubConsumer{})
	if err := w.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
