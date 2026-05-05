package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/pkg/auth"
	"golang.org/x/term"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/db"
	"github.com/jamestiberiuskirk/stackr/internal/repo/sqlite"
	"github.com/jamestiberiuskirk/stackr/internal/service"
)

const setPasswordHelp = `Usage:
  stackr set-password <email>

Sets the password for a user declared in stackr.yaml's auth.users block.
Refuses if the email is not declared — the declarative model means the
row would be deleted on next daemon boot anyway.

The password is read interactively from stdin (twice, no echo). The CLI
opens the database directly, so this command requires shell access to the
host where stackr runs.

Environment:
  STACKR_REPO_ROOT  Repo root containing stackr.yaml (default: cwd)
  DATABASE_PATH     SQLite path (default: ./data/daemon.db; matches the daemon)
`

// runSetPassword resolves the repo, opens the DB, syncs users from config,
// then prompts for and writes a new password hash for the named user.
//
// The sync step is deliberate: it makes `stackr set-password` work on a
// fresh checkout where the daemon has never booted, and guarantees that
// the user's row exists by the time we go to update it.
func runSetPassword(args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(setPasswordHelp)
		return nil
	}
	if len(args) != 1 {
		return errors.New("set-password requires exactly one argument: <email>")
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

	// Run the same sync the daemon runs on boot. After this completes, any
	// user declared in config is guaranteed to have a row in the DB; any
	// row not declared has been removed.
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

	fmt.Fprintf(os.Stderr, "Setting password for %s\n", email)
	password, err := promptPassword()
	if err != nil {
		return err
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	user.PasswordHash = hash
	user.UpdatedAt = time.Now()
	if err := store.UpdateUser(ctx, user); err != nil {
		return fmt.Errorf("update user: %w", err)
	}

	fmt.Printf("password set for %s\n", email)
	return nil
}

// promptPassword reads a password twice from the controlling terminal and
// confirms the two entries match. An asterisk is echoed per character of
// input so the operator gets visual feedback that keystrokes registered.
//
// Refuses to run when stdin isn't a terminal so a piped password
// (`echo hunter2 | stackr ...`) can't bypass the confirmation; that's also
// why there's no --password flag — passwords don't belong in shell history.
func promptPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("password input requires an interactive terminal")
	}

	fmt.Fprint(os.Stderr, "New password: ")
	first, err := readPasswordWithMask(fd)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(first) == 0 {
		return "", errors.New("password cannot be empty")
	}

	fmt.Fprint(os.Stderr, "Confirm:      ")
	second, err := readPasswordWithMask(fd)
	if err != nil {
		return "", fmt.Errorf("read confirmation: %w", err)
	}

	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}

	return string(first), nil
}

// readPasswordWithMask reads a line from the terminal, echoing one '*' per
// printable input byte and handling backspace/Ctrl-C. The terminal is put
// into raw mode for the duration so each keystroke arrives unbuffered.
//
// Multi-byte UTF-8 input prints one '*' per byte, not per rune — a fine
// trade-off for code simplicity given password input is typically ASCII.
func readPasswordWithMask(fd int) ([]byte, error) {
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	var buf []byte
	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if err != nil {
			fmt.Fprintln(os.Stderr)
			return nil, err
		}
		if n == 0 {
			continue
		}
		switch b[0] {
		case '\r', '\n':
			fmt.Fprint(os.Stderr, "\r\n")
			return buf, nil
		case 0x03: // Ctrl-C
			fmt.Fprint(os.Stderr, "\r\n")
			return nil, errors.New("interrupted")
		case 0x7F, 0x08: // DEL / Backspace
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				fmt.Fprint(os.Stderr, "\b \b")
			}
		default:
			if b[0] >= 0x20 {
				buf = append(buf, b[0])
				fmt.Fprint(os.Stderr, "*")
			}
		}
	}
}
