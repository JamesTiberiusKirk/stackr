package sqlite

import (
	"context"
	"errors"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"gorm.io/gorm"
)

func (s *Store) GetUserByID(ctx context.Context, id string) (*repo.User, error) {
	var u repo.User
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*repo.User, error) {
	var u repo.User
	err := s.db.WithContext(ctx).Where("email = ?", email).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) CreateUser(ctx context.Context, user *repo.User) error {
	return s.db.WithContext(ctx).Create(user).Error
}
