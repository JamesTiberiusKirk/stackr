package service

import (
	"context"
	"testing"
	"time"

	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/db"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/repo/sqlite"
)

// newSyncTestStore returns a freshly-migrated in-memory SQLite store. The
// raw *gorm.DB is also returned so a test can poison it (close the
// connection) to exercise store-failure paths, mirroring the pattern in
// stackr_test.go.
func newSyncTestStore(t *testing.T) (repo.Store, *gorm.DB) {
	t.Helper()
	database, err := db.ConnectContext(context.Background(), ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(database))
	return sqlite.NewStore(database), database
}

func TestPlanUserSync_AllCreatesAgainstEmptyStore(t *testing.T) {
	store, _ := newSyncTestStore(t)

	configUsers := []config.UserConfig{
		{Email: "admin@example.com", Name: "Admin", Role: "admin"},
		{Email: "ops@example.com", Name: "Ops", Role: "operator"},
	}

	plan, err := PlanUserSync(context.Background(), store, configUsers)
	require.NoError(t, err)
	require.Len(t, plan.Create, 2)
	require.Empty(t, plan.Update)
	require.Empty(t, plan.Delete)
	require.False(t, plan.IsEmpty())

	emails := []string{plan.Create[0].Email, plan.Create[1].Email}
	require.ElementsMatch(t, []string{"admin@example.com", "ops@example.com"}, emails)
	for _, u := range plan.Create {
		require.NotEmpty(t, u.ID, "Plan must allocate UUIDs so Apply can persist them deterministically")
		require.True(t, u.Active)
		require.Empty(t, u.PasswordHash, "new users must start with empty hash — login is gated until set-password runs")
	}
}

func TestPlanUserSync_NoDiffWhenAlreadyInSync(t *testing.T) {
	store, _ := newSyncTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u1", Email: "admin@example.com", Name: "Admin", Role: "admin", Active: true,
	}))

	plan, err := PlanUserSync(ctx, store,
		[]config.UserConfig{{Email: "admin@example.com", Name: "Admin", Role: "admin"}},
	)
	require.NoError(t, err)
	require.True(t, plan.IsEmpty(), "matching config and DB must produce an empty plan — boots stay fast and auditable")
}

func TestPlanUserSync_DetectsUpdatesAndDeletes(t *testing.T) {
	store, _ := newSyncTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u1", Email: "admin@example.com", Name: "OldName", Role: "admin", Active: true,
	}))
	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u2", Email: "stale@example.com", Name: "Stale", Role: "viewer", Active: true,
	}))

	plan, err := PlanUserSync(ctx, store,
		[]config.UserConfig{
			{Email: "admin@example.com", Name: "NewName", Role: "admin"},
			{Email: "fresh@example.com", Name: "Fresh", Role: "operator"},
		},
	)
	require.NoError(t, err)
	require.Len(t, plan.Create, 1)
	require.Equal(t, "fresh@example.com", plan.Create[0].Email)
	require.Len(t, plan.Update, 1)
	require.Equal(t, "u1", plan.Update[0].Existing.ID)
	require.Equal(t, "NewName", plan.Update[0].NewName)
	require.Len(t, plan.Delete, 1)
	require.Equal(t, "stale@example.com", plan.Delete[0].Email)
}

func TestPlanUserSync_NameOnlyChangeStillProducesUpdate(t *testing.T) {
	// Pins the contract that a pure rename (role unchanged) is detected.
	// Easy to break by short-circuiting the diff on role-only comparison.
	store, _ := newSyncTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u1", Email: "a@example.com", Name: "Old", Role: "admin", Active: true,
	}))

	plan, err := PlanUserSync(ctx, store,
		[]config.UserConfig{{Email: "a@example.com", Name: "New", Role: "admin"}},
	)
	require.NoError(t, err)
	require.Len(t, plan.Update, 1)
	require.Equal(t, "New", plan.Update[0].NewName)
}

func TestPlanUserSync_LowercasesEmailForStorage(t *testing.T) {
	// The login handler lowercases user input; storage must match or
	// every mixed-case YAML entry would be unloginable. This test is the
	// guard rail that pins both behaviours together.
	store, _ := newSyncTestStore(t)
	ctx := context.Background()

	plan, err := PlanUserSync(ctx, store,
		[]config.UserConfig{{Email: "Admin@Example.COM", Name: "Admin", Role: "admin"}},
	)
	require.NoError(t, err)
	require.Len(t, plan.Create, 1)
	require.Equal(t, "admin@example.com", plan.Create[0].Email,
		"Plan must lowercase email so the persisted row matches what the login handler will look up")

	require.NoError(t, ApplyUserSync(ctx, store, plan))

	// And the stored row is reachable via mixed-case lookup.
	got, err := store.GetUserByEmail(ctx, "ADMIN@example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "admin@example.com", got.Email)
}

func TestPlanUserSync_TrimsWhitespaceInConfig(t *testing.T) {
	// Defends against operators accidentally indenting YAML values; the
	// trimmed value is what gets compared against the DB and what gets
	// persisted, so an existing "admin" role in the DB matches "  admin  "
	// in the config and we don't churn an update on every boot.
	store, _ := newSyncTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u1", Email: "a@example.com", Name: "Admin", Role: "admin", Active: true,
	}))

	plan, err := PlanUserSync(ctx, store,
		[]config.UserConfig{{Email: "  a@example.com  ", Name: " Admin ", Role: "  admin  "}},
	)
	require.NoError(t, err)
	require.True(t, plan.IsEmpty())
}

func TestPlanUserSync_ValidationFailures(t *testing.T) {
	store, _ := newSyncTestStore(t)

	tests := []struct {
		name    string
		users   []config.UserConfig
		wantSub string
	}{
		{
			name:    "empty users slice",
			users:   nil,
			wantSub: "at least one user must be declared",
		},
		{
			name: "missing email",
			users: []config.UserConfig{
				{Email: "", Name: "X", Role: "admin"},
			},
			wantSub: "email is required",
		},
		{
			name: "missing role",
			users: []config.UserConfig{
				{Email: "a@example.com", Name: "X", Role: ""},
			},
			wantSub: "role is required",
		},
		{
			name: "duplicate email",
			users: []config.UserConfig{
				{Email: "a@example.com", Name: "A", Role: "admin"},
				{Email: "a@example.com", Name: "B", Role: "viewer"},
			},
			wantSub: "duplicate email",
		},
		{
			name: "duplicate email differing in case",
			users: []config.UserConfig{
				{Email: "Admin@Example.com", Name: "A", Role: "admin"},
				{Email: "admin@example.com", Name: "B", Role: "viewer"},
			},
			wantSub: "duplicate email",
		},
		{
			name: "no admin declared",
			users: []config.UserConfig{
				{Email: "a@example.com", Name: "A", Role: "viewer"},
			},
			wantSub: "at least one user with role=admin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanUserSync(context.Background(), store, tt.users)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantSub)
			require.True(t, plan.IsEmpty(), "validation failure must return an empty plan; otherwise an Apply could mutate state with bad data")
		})
	}
}

func TestPlanUserSync_PropagatesListUsersError(t *testing.T) {
	store, database := newSyncTestStore(t)

	sqlDB, err := database.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = PlanUserSync(context.Background(), store,
		[]config.UserConfig{{Email: "a@example.com", Name: "A", Role: "admin"}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "list users")
}

func TestApplyUserSync_AppliesCreatesUpdatesDeletes(t *testing.T) {
	store, _ := newSyncTestStore(t)
	ctx := context.Background()

	// Seed a row that will be updated and one that will be deleted.
	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u1", Email: "admin@example.com", Name: "OldName", Role: "admin", Active: true,
		PasswordHash: "preserved-hash", TOTPSecret: "preserved-totp", TOTPEnabled: true,
	}))
	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u2", Email: "stale@example.com", Name: "Stale", Role: "viewer", Active: true,
	}))

	plan, err := PlanUserSync(ctx, store,
		[]config.UserConfig{
			{Email: "admin@example.com", Name: "NewName", Role: "admin"},
			{Email: "new@example.com", Name: "Fresh", Role: "operator"},
		},
	)
	require.NoError(t, err)
	require.NoError(t, ApplyUserSync(ctx, store, plan))

	users, err := store.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2, "stale@example.com should be deleted, leaving admin + new")

	byEmail := map[string]repo.User{}
	for _, u := range users {
		byEmail[u.Email] = u
	}

	admin := byEmail["admin@example.com"]
	require.Equal(t, "NewName", admin.Name, "Apply must rewrite Name from config")
	require.Equal(t, "preserved-hash", admin.PasswordHash,
		"Apply must NOT touch password hash on update — that's the whole point of declarative-with-runtime-secrets")
	require.Equal(t, "preserved-totp", admin.TOTPSecret, "TOTP secret must survive an update")
	require.True(t, admin.TOTPEnabled)

	fresh := byEmail["new@example.com"]
	require.NotEmpty(t, fresh.ID)
	require.True(t, fresh.Active)
	require.Empty(t, fresh.PasswordHash)
}

func TestApplyUserSync_DeleteCascadesSessions(t *testing.T) {
	// A deleted user must not leave orphan sessions behind that authorise a
	// now-removed account. Pinned because the cascade lives in DeleteUser
	// (transactional) — easy to drop in a refactor.
	store, _ := newSyncTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u1", Email: "admin@example.com", Name: "Admin", Role: "admin", Active: true,
	}))
	require.NoError(t, store.CreateUser(ctx, &repo.User{
		ID: "u2", Email: "going@example.com", Name: "Going", Role: "viewer", Active: true,
	}))

	require.NoError(t, store.Create(ctx, &auth.Session{
		ID: "s1", SubjectID: "u2", Token: "tok-going", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}))
	require.NoError(t, store.Create(ctx, &auth.Session{
		ID: "s2", SubjectID: "u1", Token: "tok-admin", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}))

	plan, err := PlanUserSync(ctx, store,
		[]config.UserConfig{{Email: "admin@example.com", Name: "Admin", Role: "admin"}},
	)
	require.NoError(t, err)
	require.NoError(t, ApplyUserSync(ctx, store, plan))

	goneSession, err := store.GetByToken(ctx, "tok-going")
	require.NoError(t, err)
	require.Nil(t, goneSession, "the deleted user's session must be gone — otherwise the cookie still authenticates")

	keptSession, err := store.GetByToken(ctx, "tok-admin")
	require.NoError(t, err)
	require.NotNil(t, keptSession, "untouched users' sessions must survive — the cascade must scope to the deleted subject_id only")
}
