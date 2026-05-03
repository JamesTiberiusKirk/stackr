package sqlite

import (
	"context"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

func (s *Store) CreateEvent(ctx context.Context, e *repo.Event) error {
	return s.db.WithContext(ctx).Create(e).Error
}

func (s *Store) ListEvents(ctx context.Context, filter repo.EventFilter) ([]repo.Event, error) {
	q := s.db.WithContext(ctx).Order("created_at DESC")
	if filter.Kind != "" {
		q = q.Where("kind = ?", filter.Kind)
	}
	if filter.Stack != "" {
		q = q.Where("stack = ?", filter.Stack)
	}
	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		q = q.Offset(filter.Offset)
	}
	var out []repo.Event
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
