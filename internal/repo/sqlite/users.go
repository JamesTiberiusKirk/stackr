package sqlite

import (
	"context"
	"errors"
	"strings"

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

// GetUserByEmail looks up a user by email, lowercased for the comparison.
// Storage is also lowercased (see usersync.validateUserConfigs), so this is
// a defense-in-depth normalisation rather than a case-insensitive index —
// callers passing mixed-case input still find the row.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*repo.User, error) {
	var u repo.User
	err := s.db.WithContext(ctx).Where("email = ?", strings.ToLower(email)).First(&u).Error
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

func (s *Store) UpdateUser(ctx context.Context, user *repo.User) error {
	return s.db.WithContext(ctx).Save(user).Error
}

func (s *Store) ListUsers(ctx context.Context) ([]repo.User, error) {
	var users []repo.User
	if err := s.db.WithContext(ctx).Order("email").Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// DeleteUser removes the user row plus any sessions or invite tokens
// referencing them. Done in a transaction so a partial wipe can't leave
// orphan sessions authorising a now-deleted account, or stale invites that
// would let someone else claim a recreated email.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("subject_id = ?", id).Delete(&sessionRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", id).Delete(&repo.InviteToken{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&repo.User{}).Error
	})
}
