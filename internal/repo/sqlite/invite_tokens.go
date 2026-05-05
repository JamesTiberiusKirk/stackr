package sqlite

import (
	"context"
	"errors"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"gorm.io/gorm"
)

func (s *Store) CreateInviteToken(ctx context.Context, t *repo.InviteToken) error {
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *Store) GetInviteTokenByToken(ctx context.Context, token string) (*repo.InviteToken, error) {
	var row repo.InviteToken
	err := s.db.WithContext(ctx).Where("token = ?", token).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// MarkInviteUsed flips Used=true on the row identified by id. Used (rather
// than DELETE) so a redeemed token leaves a trail — useful for auditing
// "who set their password and when".
func (s *Store) MarkInviteUsed(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).
		Model(&repo.InviteToken{}).
		Where("id = ?", id).
		Update("used", true).Error
}
