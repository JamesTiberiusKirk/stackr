package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/FyrmForge/hamr/pkg/auth"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// ErrInvalidCredentials is returned when authentication fails — wrong email,
// wrong password, or a user whose PasswordHash is still empty (declared but
// not yet set via `stackr set-password`).
var ErrInvalidCredentials = errors.New("invalid credentials")

// AuthService handles authentication logic. Users are not created here —
// they're declared in stackr.yaml and synced on boot. This service only
// verifies credentials against existing rows.
type AuthService struct {
	store repo.Store
}

// NewAuthService creates a new auth service.
func NewAuthService(store repo.Store) *AuthService {
	return &AuthService{store: store}
}

// Authenticate verifies credentials and returns the user. A user with an
// empty PasswordHash always fails — Argon2id can't verify against empty —
// so declared-but-unset accounts are non-loginable until set-password runs.
func (s *AuthService) Authenticate(ctx context.Context, email, password string) (*repo.User, error) {
	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}

	ok, err := auth.CheckPassword(password, user.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("check password: %w", err)
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}

	return user, nil
}
