package repository

import (
	"context"
	"errors"

	"gophprofile/internal/domain"
	"gophprofile/pkg/circuitbreaker"
)

// AvatarStore - операции с метаданными аватарок в хранилище.
type AvatarStore interface {
	Create(ctx context.Context, a domain.Avatar) error
	GetByID(ctx context.Context, id string) (*domain.Avatar, error)
	GetLatestByUserID(ctx context.Context, userID string) (*domain.Avatar, error)
	ListByUserID(ctx context.Context, userID string) ([]domain.Avatar, error)
	SoftDelete(ctx context.Context, id string) error
	ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error)
	GetByIDIncludingDeleted(ctx context.Context, id string) (*domain.Avatar, error)
	UpdateProcessingResult(ctx context.Context, id string, thumbs map[string]string, status string) error
	MarkProcessingFailed(ctx context.Context, id string) error
}

// BreakingRepository проксирует AvatarStore через circuit breaker.
// domain.ErrNotFound не считается сбоем зависимости.
type BreakingRepository struct {
	inner AvatarStore
	cb    *circuitbreaker.Breaker
}

// NewBreakingRepository создаёт обёртку над AvatarStore.
func NewBreakingRepository(inner AvatarStore, cb *circuitbreaker.Breaker) *BreakingRepository {
	return &BreakingRepository{inner: inner, cb: cb}
}

func (r *BreakingRepository) finish(err error) error {
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		r.cb.Failure()
		return err
	}
	r.cb.Success()
	return err
}

func (r *BreakingRepository) Create(ctx context.Context, a domain.Avatar) error {
	if err := r.cb.Allow(); err != nil {
		return err
	}
	return r.finish(r.inner.Create(ctx, a))
}

func (r *BreakingRepository) GetByID(ctx context.Context, id string) (*domain.Avatar, error) {
	if err := r.cb.Allow(); err != nil {
		return nil, err
	}
	a, err := r.inner.GetByID(ctx, id)
	return a, r.finish(err)
}

func (r *BreakingRepository) GetLatestByUserID(ctx context.Context, userID string) (*domain.Avatar, error) {
	if err := r.cb.Allow(); err != nil {
		return nil, err
	}
	a, err := r.inner.GetLatestByUserID(ctx, userID)
	return a, r.finish(err)
}

func (r *BreakingRepository) ListByUserID(ctx context.Context, userID string) ([]domain.Avatar, error) {
	if err := r.cb.Allow(); err != nil {
		return nil, err
	}
	items, err := r.inner.ListByUserID(ctx, userID)
	return items, r.finish(err)
}

func (r *BreakingRepository) SoftDelete(ctx context.Context, id string) error {
	if err := r.cb.Allow(); err != nil {
		return err
	}
	return r.finish(r.inner.SoftDelete(ctx, id))
}

func (r *BreakingRepository) ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error) {
	if err := r.cb.Allow(); err != nil {
		return nil, err
	}
	a, err := r.inner.ClaimForProcessing(ctx, id)
	return a, r.finish(err)
}

func (r *BreakingRepository) GetByIDIncludingDeleted(ctx context.Context, id string) (*domain.Avatar, error) {
	if err := r.cb.Allow(); err != nil {
		return nil, err
	}
	a, err := r.inner.GetByIDIncludingDeleted(ctx, id)
	return a, r.finish(err)
}

func (r *BreakingRepository) UpdateProcessingResult(ctx context.Context, id string, thumbs map[string]string, status string) error {
	if err := r.cb.Allow(); err != nil {
		return err
	}
	return r.finish(r.inner.UpdateProcessingResult(ctx, id, thumbs, status))
}

func (r *BreakingRepository) MarkProcessingFailed(ctx context.Context, id string) error {
	if err := r.cb.Allow(); err != nil {
		return err
	}
	return r.finish(r.inner.MarkProcessingFailed(ctx, id))
}
