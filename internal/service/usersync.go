package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// RoleAdmin is the role name the daemon requires at least one declared user
// to hold — without it no one can manage users, which would lock the system.
const RoleAdmin = "admin"

// UserSyncPlan is the diff between desired (config) and actual (DB) user
// state. The split into Plan + Apply is deliberate: a future "approve before
// apply" mode (see project memory on the plan/apply model) will persist this
// struct, render it for an admin, and run Apply only after explicit consent.
type UserSyncPlan struct {
	Create []repo.User
	Update []UserUpdateDiff
	Delete []repo.User
}

// UserUpdateDiff captures the name/role columns the config can change. It
// carries the existing row whole so Apply can preserve PasswordHash, TOTP
// state, Active, and timestamps — runtime data the config has no opinion on.
type UserUpdateDiff struct {
	Existing repo.User
	NewName  string
	NewRole  string
}

// IsEmpty reports whether applying the plan would mutate anything. Useful
// for skipping logs/transactions on no-op boots.
func (p UserSyncPlan) IsEmpty() bool {
	return len(p.Create) == 0 && len(p.Update) == 0 && len(p.Delete) == 0
}

// PlanUserSync computes the diff between configUsers (desired) and the
// store's current users (actual). It validates the config first and returns
// a non-nil error on any invariant violation; in that case the plan is
// empty. Apply must not be called when an error was returned.
//
// The function performs no mutations — it only reads from the store.
func PlanUserSync(ctx context.Context, store repo.Store, configUsers []config.UserConfig) (UserSyncPlan, error) {
	normalised, err := validateUserConfigs(configUsers)
	if err != nil {
		return UserSyncPlan{}, err
	}

	existing, err := store.ListUsers(ctx)
	if err != nil {
		return UserSyncPlan{}, fmt.Errorf("list users: %w", err)
	}

	byEmail := make(map[string]repo.User, len(existing))
	for _, u := range existing {
		byEmail[u.Email] = u
	}

	var plan UserSyncPlan
	seen := make(map[string]struct{}, len(normalised))

	for _, cu := range normalised {
		seen[cu.Email] = struct{}{}
		if existingUser, ok := byEmail[cu.Email]; ok {
			if existingUser.Name != cu.Name || existingUser.Role != cu.Role {
				plan.Update = append(plan.Update, UserUpdateDiff{
					Existing: existingUser,
					NewName:  cu.Name,
					NewRole:  cu.Role,
				})
			}
			continue
		}
		plan.Create = append(plan.Create, repo.User{
			ID:     uuid.New().String(),
			Email:  cu.Email,
			Name:   cu.Name,
			Role:   cu.Role,
			Active: true,
		})
	}

	for _, u := range existing {
		if _, ok := seen[u.Email]; !ok {
			plan.Delete = append(plan.Delete, u)
		}
	}

	return plan, nil
}

// ApplyUserSync realises the plan against the store. Operations happen in
// Create → Update → Delete order. Each step's error is wrapped with the
// affected email so logs pinpoint the failing row.
func ApplyUserSync(ctx context.Context, store repo.Store, plan UserSyncPlan) error {
	now := time.Now()
	for i := range plan.Create {
		u := plan.Create[i]
		u.CreatedAt = now
		u.UpdatedAt = now
		if err := store.CreateUser(ctx, &u); err != nil {
			return fmt.Errorf("create user %q: %w", u.Email, err)
		}
	}
	for _, diff := range plan.Update {
		updated := diff.Existing
		updated.Name = diff.NewName
		updated.Role = diff.NewRole
		updated.UpdatedAt = now
		if err := store.UpdateUser(ctx, &updated); err != nil {
			return fmt.Errorf("update user %q: %w", updated.Email, err)
		}
	}
	for _, u := range plan.Delete {
		if err := store.DeleteUser(ctx, u.ID); err != nil {
			return fmt.Errorf("delete user %q: %w", u.Email, err)
		}
	}
	return nil
}

// validateUserConfigs enforces:
//   - non-empty email and role on every entry
//   - unique emails within the config (case-insensitive)
//   - at least one user with role=admin (no admin = no one can recover)
//
// Returned UserConfigs have email lowercased — the login handler also
// lowercases its input, so storage must match or lookups would miss for any
// user whose YAML entry uses mixed case. Name and role are trimmed but not
// case-folded; role comparison against "admin" is exact for now (custom
// roles from auth.roles in step #4 will land additively).
func validateUserConfigs(users []config.UserConfig) ([]config.UserConfig, error) {
	if len(users) == 0 {
		return nil, errors.New("auth.users: at least one user must be declared")
	}
	out := make([]config.UserConfig, 0, len(users))
	seen := make(map[string]struct{}, len(users))
	hasAdmin := false
	for i, u := range users {
		email := strings.ToLower(strings.TrimSpace(u.Email))
		role := strings.TrimSpace(u.Role)
		name := strings.TrimSpace(u.Name)
		if email == "" {
			return nil, fmt.Errorf("auth.users[%d]: email is required", i)
		}
		if role == "" {
			return nil, fmt.Errorf("auth.users[%d] (%s): role is required", i, email)
		}
		if _, dup := seen[email]; dup {
			return nil, fmt.Errorf("auth.users: duplicate email %q", email)
		}
		seen[email] = struct{}{}
		if role == RoleAdmin {
			hasAdmin = true
		}
		out = append(out, config.UserConfig{Email: email, Name: name, Role: role})
	}
	if !hasAdmin {
		return nil, errors.New("auth.users: at least one user with role=admin is required")
	}
	return out, nil
}
