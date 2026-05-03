package sqlite

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

func (s *Store) CreateCronExecution(ctx context.Context, e *repo.CronExecution) error {
	return s.db.WithContext(ctx).Create(e).Error
}

func (s *Store) UpdateCronExecution(ctx context.Context, e *repo.CronExecution) error {
	return s.db.WithContext(ctx).Save(e).Error
}

func (s *Store) GetCronExecutionByID(ctx context.Context, id string) (*repo.CronExecution, error) {
	var e repo.CronExecution
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&e).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

func (s *Store) ListCronExecutions(ctx context.Context, filter repo.CronExecutionFilter) ([]repo.CronExecution, error) {
	q := s.db.WithContext(ctx).Order("started_at DESC")
	if filter.Stack != "" {
		q = q.Where("stack = ?", filter.Stack)
	}
	if filter.Service != "" {
		q = q.Where("service = ?", filter.Service)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		q = q.Offset(filter.Offset)
	}
	var out []repo.CronExecution
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
