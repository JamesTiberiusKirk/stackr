package repo

import "time"

// InviteToken is a one-time-use credential an admin issues for a declared
// user so they can set their initial password without SSH access. The
// token is the user-facing secret; UserID binds it to its declared row,
// which is the source of identity.
//
// Lifecycle: issued (Used=false), redeemed once (Used=true). Multiple
// outstanding tokens per user are allowed — admin reissue is non-destructive
// to prior tokens, but each token only redeems once.
type InviteToken struct {
	ID        string    `db:"id"`
	UserID    string    `db:"user_id"`
	Token     string    `db:"token"`
	ExpiresAt time.Time `db:"expires_at"`
	Used      bool      `db:"used"`
	CreatedAt time.Time `db:"created_at"`
}
