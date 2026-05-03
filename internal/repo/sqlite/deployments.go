package sqlite

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

func (s *Store) CreateDeployment(ctx context.Context, d *repo.Deployment) error {
	return s.db.WithContext(ctx).Create(d).Error
}

func (s *Store) UpdateDeployment(ctx context.Context, d *repo.Deployment) error {
	return s.db.WithContext(ctx).Save(d).Error
}

func (s *Store) GetDeploymentByID(ctx context.Context, id string) (*repo.Deployment, error) {
	var d repo.Deployment
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

func (s *Store) ListDeployments(ctx context.Context, filter repo.DeploymentFilter) ([]repo.Deployment, error) {
	q := s.db.WithContext(ctx).Order("started_at DESC")
	if filter.Stack != "" {
		q = q.Where("stack = ?", filter.Stack)
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
	var out []repo.Deployment
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
