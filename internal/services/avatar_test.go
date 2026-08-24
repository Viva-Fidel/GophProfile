package services

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"gophprofile/internal/domain"
)

type memRepo struct {
	items map[string]domain.Avatar
}

func newMemRepo() *memRepo {
	return &memRepo{items: map[string]domain.Avatar{}}
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
	var found *domain.Avatar
	for i := range m.items {
		a := m.items[i]
		if a.UserID == userID && a.DeletedAt == nil {
			cp := a
			if found == nil || a.CreatedAt.After(found.CreatedAt) {
				found = &cp
			}
		}
	}
	if found == nil {
		return nil, domain.ErrNotFound
	}
	return found, nil
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
	now := a.UpdatedAt
	a.DeletedAt = &now
	m.items[id] = a
	return nil
}

func (m *memRepo) ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error) {
	return m.GetByID(ctx, id)
}

func (m *memRepo) UpdateProcessingResult(_ context.Context, id string, thumbs map[string]string, status string) error {
	a, ok := m.items[id]
	if !ok {
		return domain.ErrNotFound
	}
	a.ThumbnailS3Keys = thumbs
	a.ProcessingStatus = status
	m.items[id] = a
	return nil
}

func (m *memRepo) MarkProcessingFailed(_ context.Context, id string) error {
	a, ok := m.items[id]
	if !ok {
		return domain.ErrNotFound
	}
	a.ProcessingStatus = domain.ProcessingFailed
	m.items[id] = a
	return nil
}

type memStore struct {
	objects map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{objects: map[string][]byte{}}
}

func (s *memStore) Upload(_ context.Context, key string, data []byte, _ string) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.objects[key] = cp
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

type memPub struct {
	keys []string
	err  error
}

func (p *memPub) Publish(_ context.Context, routingKey, _ string, _ []byte) error {
	if p.err != nil {
		return p.err
	}
	p.keys = append(p.keys, routingKey)
	return nil
}

func (p *memPub) Ping(context.Context) error { return nil }
func (p *memPub) Close() error               { return nil }

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAvatarServiceUploadGetDelete(t *testing.T) {
	repo := newMemRepo()
	store := newMemStore()
	pub := &memPub{}
	svc := NewAvatarService(repo, store, pub, "http://localhost:8080")

	if _, err := svc.Upload(context.Background(), "", "a.png", pngBytes(t)); err != domain.ErrMissingUser {
		t.Fatal(err)
	}
	if _, err := svc.Upload(context.Background(), "u1", "a.png", nil); err != domain.ErrMissingFile {
		t.Fatal(err)
	}
	tooBig := make([]byte, domain.MaxUploadBytes+1)
	if _, err := svc.Upload(context.Background(), "u1", "a.png", tooBig); err != domain.ErrTooLarge {
		t.Fatal(err)
	}
	if _, err := svc.Upload(context.Background(), "u1", "a.png", []byte("nope")); err != domain.ErrInvalidFormat {
		t.Fatal(err)
	}

	av, err := svc.Upload(context.Background(), "u1", "a.png", pngBytes(t))
	if err != nil || av.UserID != "u1" || av.PublicStatus() != domain.StatusProcessing {
		t.Fatal(err, av)
	}
	if len(pub.keys) != 1 || pub.keys[0] != domain.RoutingUploaded {
		t.Fatal(pub.keys)
	}
	if svc.AvatarURL(av.ID) != "http://localhost:8080/api/v1/avatars/"+av.ID {
		t.Fatal(svc.AvatarURL(av.ID))
	}

	got, err := svc.Get(context.Background(), av.ID)
	if err != nil || got.ID != av.ID {
		t.Fatal(err, got)
	}
	list, err := svc.ListByUser(context.Background(), "u1")
	if err != nil || len(list) != 1 {
		t.Fatal(err, list)
	}
	latest, err := svc.GetLatestByUser(context.Background(), "u1")
	if err != nil || latest.ID != av.ID {
		t.Fatal(err, latest)
	}

	data, ctype, etag, err := svc.File(context.Background(), av, "", "")
	if err != nil || len(data) == 0 || ctype == "" || etag == "" {
		t.Fatal(err, len(data), ctype, etag)
	}
	converted, mime, _, err := svc.File(context.Background(), av, domain.SizeOriginal, "jpeg")
	if err != nil || mime != "image/jpeg" || len(converted) == 0 {
		t.Fatal(err, mime)
	}
	_, _, _, err = svc.File(context.Background(), av, domain.Size100, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.File(context.Background(), av, "50x50", ""); err != domain.ErrInvalidSize {
		t.Fatal(err)
	}

	if err := svc.Delete(context.Background(), "other", av.ID); err != domain.ErrForbidden {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), "u1", av.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), av.ID); err != domain.ErrNotFound {
		t.Fatal(err)
	}
}

func TestAvatarServiceDeleteUserAvatar(t *testing.T) {
	repo := newMemRepo()
	store := newMemStore()
	svc := NewAvatarService(repo, store, &memPub{}, "http://localhost:8080")
	av, err := svc.Upload(context.Background(), "u1", "a.png", pngBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUserAvatar(context.Background(), "u2", "u1"); err != domain.ErrForbidden {
		t.Fatal(err)
	}
	if err := svc.DeleteUserAvatar(context.Background(), "u1", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(context.Background(), av.ID); err != domain.ErrNotFound {
		t.Fatal(err)
	}
}

func TestSanitizeFileName(t *testing.T) {
	if sanitizeFileName("../x.png") != "x.png" {
		t.Fatal(sanitizeFileName("../x.png"))
	}
	if sanitizeFileName("") == "" {
		t.Fatal("empty")
	}
}
