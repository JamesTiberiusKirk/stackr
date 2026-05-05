package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// InviteTTL is the validity window for a freshly issued invite. Long enough
// for an admin to send the link via Slack/email and the user to redeem it
// across a weekend; short enough that a leaked token isn't permanent.
const InviteTTL = 7 * 24 * time.Hour

// inviteTokenBytes is the entropy in each generated token (32 bytes ≈ 256
// bits, base64url-encoded → 43 chars). Far beyond brute-force range; the
// length matters because the token is the only thing standing between an
// attacker and a password-set on a known account.
const inviteTokenBytes = 32

// Invite-flow sentinels. They're public so the web handler can map them to
// 401/403/410 status codes without string-matching error text.
var (
	ErrInviteNotFound = errors.New("invite token not found")
	ErrInviteUsed     = errors.New("invite token has already been used")
	ErrInviteExpired  = errors.New("invite token has expired")
	ErrUserNotFound   = errors.New("user not found")
)

// InviteService issues and redeems invite tokens. It's the runtime
// counterpart to the declarative user sync — sync says "this user exists";
// the invite flow says "this user can finally log in".
type InviteService struct {
	store repo.Store
	now   func() time.Time
}

// NewInviteService wires the service to a store. now defaults to time.Now;
// tests inject a fake clock to exercise expiry without sleeping.
func NewInviteService(store repo.Store) *InviteService {
	return &InviteService{store: store, now: time.Now}
}

// IssueInvite mints a fresh token for the user identified by email. Returns
// the token (caller is responsible for delivering it) and its absolute
// expiry. Multiple outstanding invites per user are allowed; reissue does
// not invalidate prior tokens — each is independently one-time-use.
func (s *InviteService) IssueInvite(ctx context.Context, email string) (token string, expiresAt time.Time, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("lookup user: %w", err)
	}
	if user == nil {
		return "", time.Time{}, ErrUserNotFound
	}

	tok, err := generateToken()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate token: %w", err)
	}

	now := s.now()
	row := &repo.InviteToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     tok,
		ExpiresAt: now.Add(InviteTTL),
		CreatedAt: now,
	}
	if err := s.store.CreateInviteToken(ctx, row); err != nil {
		return "", time.Time{}, fmt.Errorf("persist invite: %w", err)
	}
	return tok, row.ExpiresAt, nil
}

// RedeemInvite validates the token, sets the user's password, and marks the
// token used. Atomicity is application-level: if MarkInviteUsed fails after
// a successful UpdateUser, the password is set but the token is still
// re-redeemable until expiry — accepted trade-off; the worst case is a
// re-set with the same hash.
//
// Redemption works whether the user has a password already or not, so
// invites double as an admin-assisted password reset.
func (s *InviteService) RedeemInvite(ctx context.Context, token, password string) (*repo.User, error) {
	if password == "" {
		return nil, errors.New("password cannot be empty")
	}

	row, err := s.store.GetInviteTokenByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("lookup invite: %w", err)
	}
	if row == nil {
		return nil, ErrInviteNotFound
	}
	if row.Used {
		return nil, ErrInviteUsed
	}
	if !s.now().Before(row.ExpiresAt) {
		return nil, ErrInviteExpired
	}

	user, err := s.store.GetUserByID(ctx, row.UserID)
	if err != nil {
		return nil, fmt.Errorf("lookup user: %w", err)
	}
	if user == nil {
		// Defensive: cascade in DeleteUser should remove tokens with the
		// user, so this branch is only reachable if a token outlived its
		// owner via a bug.
		return nil, ErrUserNotFound
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user.PasswordHash = hash
	user.UpdatedAt = s.now()
	if err := s.store.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}

	if err := s.store.MarkInviteUsed(ctx, row.ID); err != nil {
		return nil, fmt.Errorf("mark used: %w", err)
	}
	return user, nil
}

// generateToken returns a base64url-encoded random token. base64url (no
// padding) is URL-safe, so the token can be embedded in /accept-invite?token=
// without any further encoding step.
func generateToken() (string, error) {
	buf := make([]byte, inviteTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
