package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/db"
	"github.com/jamestiberiuskirk/stackr/internal/repo/sqlite"
	"github.com/jamestiberiuskirk/stackr/internal/service"
)

const issueInviteHelp = `Usage:
  stackr issue-invite <email>

Issues a one-time invite token for a user declared in stackr.yaml's
auth.users block. The token is printed to stdout — copy it to the user via
whatever channel you trust (Slack, email, sneakernet). The user redeems it
at /accept-invite?token=<token> in the daemon's web UI.

Refuses if the email is not declared. Multiple outstanding invites per user
are allowed; each is independently one-time-use. Token validity: 7 days.

Environment:
  STACKR_REPO_ROOT  Repo root containing stackr.yaml (default: cwd)
  DATABASE_PATH     SQLite path (default: ./data/daemon.db; matches the daemon)
  BASE_URL          Base URL to print in the redemption hint (optional)
`

// runIssueInvite mints an invite for a config-declared user and prints the
// token plus a copy-paste redemption URL. Like set-password, it opens the
// DB directly so it works on a fresh checkout — runs the same boot-time
// sync to guarantee the user row exists.
func runIssueInvite(args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(issueInviteHelp)
		return nil
	}
	if len(args) != 1 {
		return errors.New("issue-invite requires exactly one argument: <email>")
	}

	email := strings.TrimSpace(args[0])
	if email == "" {
		return errors.New("email is required")
	}

	repoRoot, err := config.ResolveRepoRoot(strings.TrimSpace(os.Getenv("STACKR_REPO_ROOT")))
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}

	cfg, err := config.LoadForCLI(repoRoot)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	dbPath := strings.TrimSpace(os.Getenv("DATABASE_PATH"))
	if dbPath == "" {
		dbPath = "./data/daemon.db"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database, err := db.ConnectContext(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("connect db at %s: %w", dbPath, err)
	}
	if err := db.AutoMigrate(database); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	store := sqlite.NewStore(database)

	plan, err := service.PlanUserSync(ctx, store, cfg.Global.Auth.Users)
	if err != nil {
		return fmt.Errorf("user sync plan: %w", err)
	}
	if err := service.ApplyUserSync(ctx, store, plan); err != nil {
		return fmt.Errorf("user sync apply: %w", err)
	}

	user, err := store.GetUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("lookup user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user %q is not declared in stackr.yaml auth.users — declare them first, then re-run", email)
	}

	inviter := service.NewInviteService(store)
	token, expiresAt, err := inviter.IssueInvite(ctx, email)
	if err != nil {
		return fmt.Errorf("issue invite: %w", err)
	}

	fmt.Println("invite issued")
	fmt.Printf("  user:    %s\n", email)
	fmt.Printf("  expires: %s\n", expiresAt.Format(time.RFC3339))
	fmt.Printf("  token:   %s\n", token)
	fmt.Println()

	if base := strings.TrimSpace(os.Getenv("BASE_URL")); base != "" {
		fmt.Printf("Send the user this URL:\n  %s/accept-invite?token=%s\n",
			strings.TrimRight(base, "/"), token)
	} else {
		fmt.Printf("Send the user this path on the daemon (prepend your base URL):\n  /accept-invite?token=%s\n", token)
	}
	return nil
}
