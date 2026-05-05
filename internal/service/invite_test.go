package service

import (
	"context"
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/stretchr/testify/require"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// newInviteTest sets up an in-memory SQLite store, syncs one declared user
// (admin@example.com), and returns the InviteService plus the synced user.
// A frozen-clock variant is used so expiry can be controlled precisely
// without sleeping.
func newInviteTest(t *testing.T) (*InviteService, repo.Store, *repo.User, *time.Time) {
	t.Helper()
	store, _ := newSyncTestStore(t)
	ctx := context.Background()

	plan, err := PlanUserSync(ctx, store, []config.UserConfig{
		{Email: "admin@example.com", Name: "Admin", Role: "admin"},
	})
	require.NoError(t, err)
	require.NoError(t, ApplyUserSync(ctx, store, plan))

	user, err := store.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	require.NotNil(t, user)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc := NewInviteService(store)
	svc.now = func() time.Time { return now }
	return svc, store, user, &now
}

func TestIssueInvite_PersistsTokenForDeclaredUser(t *testing.T) {
	svc, store, user, now := newInviteTest(t)
	ctx := context.Background()

	token, expiresAt, err := svc.IssueInvite(ctx, "admin@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, now.Add(InviteTTL), expiresAt,
		"expiry must be exactly issue-time + InviteTTL — callers display this verbatim")

	row, err := store.GetInviteTokenByToken(ctx, token)
	require.NoError(t, err)
	require.NotNil(t, row, "issued token must be persisted, otherwise redemption can never succeed")
	require.Equal(t, user.ID, row.UserID)
	require.False(t, row.Used)
}

func TestIssueInvite_AcceptsMixedCaseEmail(t *testing.T) {
	// The CLI/API caller may pass mixed-case input; storage is lowercased
	// so the lookup must normalise too. Same contract as the login handler.
	svc, _, _, _ := newInviteTest(t)
	_, _, err := svc.IssueInvite(context.Background(), "Admin@Example.COM")
	require.NoError(t, err)
}

func TestIssueInvite_RejectsUnknownEmail(t *testing.T) {
	svc, _, _, _ := newInviteTest(t)
	_, _, err := svc.IssueInvite(context.Background(), "ghost@example.com")
	require.ErrorIs(t, err, ErrUserNotFound,
		"declared-only invariant: an undeclared email must fail loudly with the typed sentinel so callers can map to 404")
}

func TestRedeemInvite_SetsPasswordAndMarksUsed(t *testing.T) {
	svc, store, user, _ := newInviteTest(t)
	ctx := context.Background()

	token, _, err := svc.IssueInvite(ctx, "admin@example.com")
	require.NoError(t, err)

	redeemed, err := svc.RedeemInvite(ctx, token, "hunter2-strong-pass")
	require.NoError(t, err)
	require.Equal(t, user.ID, redeemed.ID)

	// Password actually verifies.
	stored, err := store.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.PasswordHash)
	ok, err := auth.CheckPassword("hunter2-strong-pass", stored.PasswordHash)
	require.NoError(t, err)
	require.True(t, ok, "the redeemed password must verify against the persisted hash — the whole point of the flow")

	// Token is now Used.
	row, err := store.GetInviteTokenByToken(ctx, token)
	require.NoError(t, err)
	require.True(t, row.Used, "redeemed token must be marked Used so it can't be replayed")
}

func TestRedeemInvite_DoublesAsPasswordReset(t *testing.T) {
	// Pinned: a user with an existing password can still redeem an invite.
	// Reason: a single flow ("admin issues invite → user sets password")
	// covers both first-login bootstrap AND forgotten-password reset.
	svc, store, user, _ := newInviteTest(t)
	ctx := context.Background()

	user.PasswordHash = "pre-existing-hash"
	require.NoError(t, store.UpdateUser(ctx, user))

	token, _, err := svc.IssueInvite(ctx, "admin@example.com")
	require.NoError(t, err)

	_, err = svc.RedeemInvite(ctx, token, "new-password")
	require.NoError(t, err)

	stored, err := store.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	require.NotEqual(t, "pre-existing-hash", stored.PasswordHash, "redeem must overwrite the prior hash")
}

func TestRedeemInvite_RejectsBadToken(t *testing.T) {
	svc, _, _, _ := newInviteTest(t)
	_, err := svc.RedeemInvite(context.Background(), "not-a-real-token", "any-password")
	require.ErrorIs(t, err, ErrInviteNotFound)
}

func TestRedeemInvite_RejectsAlreadyUsed(t *testing.T) {
	svc, _, _, _ := newInviteTest(t)
	ctx := context.Background()

	token, _, err := svc.IssueInvite(ctx, "admin@example.com")
	require.NoError(t, err)
	_, err = svc.RedeemInvite(ctx, token, "first-pass-1234")
	require.NoError(t, err)

	_, err = svc.RedeemInvite(ctx, token, "second-pass-5678")
	require.ErrorIs(t, err, ErrInviteUsed,
		"second redeem must fail — otherwise a leaked token works repeatedly even after the user resets their password")
}

func TestRedeemInvite_RejectsExpiredToken(t *testing.T) {
	svc, _, _, now := newInviteTest(t)
	ctx := context.Background()

	token, _, err := svc.IssueInvite(ctx, "admin@example.com")
	require.NoError(t, err)

	// Jump forward past expiry.
	*now = now.Add(InviteTTL).Add(time.Second)

	_, err = svc.RedeemInvite(ctx, token, "any-password")
	require.ErrorIs(t, err, ErrInviteExpired)
}

func TestRedeemInvite_RejectsEmptyPassword(t *testing.T) {
	svc, _, _, _ := newInviteTest(t)
	ctx := context.Background()

	token, _, err := svc.IssueInvite(ctx, "admin@example.com")
	require.NoError(t, err)

	_, err = svc.RedeemInvite(ctx, token, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "password cannot be empty")
}

func TestDeleteUser_CascadesToInviteTokens(t *testing.T) {
	// Pinned because it's the easiest cascade to forget on a refactor: the
	// session cascade is well-trafficked, the invite_tokens cascade is new.
	// A leftover token after delete would let someone redeem and resurrect
	// access to a recreated email.
	svc, store, user, _ := newInviteTest(t)
	ctx := context.Background()

	token, _, err := svc.IssueInvite(ctx, "admin@example.com")
	require.NoError(t, err)
	require.NoError(t, store.DeleteUser(ctx, user.ID))

	row, err := store.GetInviteTokenByToken(ctx, token)
	require.NoError(t, err)
	require.Nil(t, row, "deleting a user must wipe their pending invites")
}
