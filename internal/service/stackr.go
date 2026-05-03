package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/runner"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// ErrStackNotFound is returned when a named stack does not exist or is not a valid stack directory.
var ErrStackNotFound = errors.New("stack not found")

// ErrInvalidStackName is returned when a submitted stack name fails validation
// (path traversal characters, whitespace, reserved literals). Distinct from
// ErrStackNotFound so the API layer can return 400 (bad request) vs 404
// (not found) — 404 here would tell the client to retry with another name,
// when really the input is the problem.
var ErrInvalidStackName = errors.New("invalid stack name")

// ErrAutoDeployDisabled is returned when a stack opts out of automated deployment via compose label.
var ErrAutoDeployDisabled = errors.New("auto-deployment is disabled for this stack")

// ErrInvalidTag is returned when the requested tag is not "latest" or a valid semver string.
var ErrInvalidTag = errors.New("tag must be \"latest\" or semver format (v1.2.3 or v1.2.3-prerelease)")

// semverPattern matches the same tag shape the legacy daemon accepts so the
// CLI can talk to either daemon without changing client-side validation.
var semverPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[a-zA-Z0-9._-]+)?$`)

// StackrService wraps stackr configuration, stack discovery, and the deploy
// runner for the daemon. It is the single point handlers go through, so the
// underlying internal/* packages stay shielded from HTTP concerns.
type StackrService struct {
	cfg    config.Config
	store  repo.Store
	runner *runner.Runner
}

// NewStackrService builds a StackrService from a loaded stackr config and the
// repo store (used for deployment / event lookups keyed by stack name).
func NewStackrService(cfg config.Config, store repo.Store) *StackrService {
	return &StackrService{
		cfg:    cfg,
		store:  store,
		runner: runner.New(cfg),
	}
}

// Config returns the underlying stackr configuration.
// Handlers should prefer the dedicated accessors (Token, StacksDir, …) over
// reaching into this directly; it's exposed mainly so the runner / cron / watch
// services can reuse the same loaded config.
func (s *StackrService) Config() config.Config {
	return s.cfg
}

// Token returns the Bearer token configured for the daemon.
func (s *StackrService) Token() string {
	return s.cfg.Token
}

// ListStacks discovers all configured stacks (local and remote) under StacksDir.
func (s *StackrService) ListStacks(_ context.Context) ([]stackcmd.StackInfo, error) {
	stacks, err := stackcmd.DiscoverStacks(s.cfg)
	if err != nil {
		return nil, fmt.Errorf("discover stacks: %w", err)
	}
	return stacks, nil
}

// GetStack resolves a single stack by name. Returns ErrInvalidStackName for
// malformed input (path traversal, whitespace, reserved names) or
// ErrStackNotFound when the name is well-formed but no stack lives at that
// path. The two are distinct so the API layer can map them to 400 vs 404.
//
// Both branches use double-%w so callers get back BOTH the service-level
// sentinel AND the original stackcmd error in the chain — that matters for
// diagnostics (the stackcmd error has the offending name) and for tests
// that want to assert the underlying cause.
func (s *StackrService) GetStack(_ context.Context, name string) (*stackcmd.StackInfo, error) {
	info, err := stackcmd.ResolveStackPath(s.cfg, name)
	if err != nil {
		if errors.Is(err, stackcmd.ErrInvalidStackName) {
			return nil, fmt.Errorf("%w: %w", ErrInvalidStackName, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrStackNotFound, err)
	}
	return &info, nil
}

// Deploy runs a deployment for the named stack at the given tag, recording a
// Deployment row in the store with status/stdout/error before returning. The
// returned *repo.Deployment reflects the final state — callers can render it
// directly as the API response. trigger should be one of the
// repo.DeploymentTrigger* constants.
func (s *StackrService) Deploy(ctx context.Context, stackName, tag, trigger string) (*repo.Deployment, error) {
	if _, err := s.GetStack(ctx, stackName); err != nil {
		return nil, err
	}

	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, fmt.Errorf("tag is required")
	}
	if tag != "latest" && !semverPattern.MatchString(tag) {
		return nil, ErrInvalidTag
	}

	enabled, err := stackcmd.IsAutoDeployEnabled(s.cfg, stackName)
	if err != nil {
		return nil, fmt.Errorf("check auto-deploy: %w", err)
	}
	if !enabled {
		return nil, ErrAutoDeployDisabled
	}

	stackCfg := config.StackConfig{
		TagEnv: strings.ToUpper(stackName) + "_IMAGE_TAG",
		Args:   []string{stackName, "update"},
	}

	now := time.Now().UTC()
	d := &repo.Deployment{
		ID:        uuid.New().String(),
		Stack:     stackName,
		Tag:       tag,
		Status:    repo.DeploymentStatusRunning,
		Trigger:   trigger,
		StartedAt: now,
	}
	if err := s.store.CreateDeployment(ctx, d); err != nil {
		return nil, fmt.Errorf("create deployment row: %w", err)
	}

	result, runErr := s.runner.Deploy(ctx, stackName, stackCfg, tag)

	finished := time.Now().UTC()
	d.FinishedAt = &finished
	if runErr != nil {
		d.Status = repo.DeploymentStatusFailed
		d.Error = runErr.Error()
		var cmdErr *runner.CommandError
		if errors.As(runErr, &cmdErr) {
			d.Stdout = cmdErr.Stdout
		}
	} else {
		d.Status = repo.DeploymentStatusSuccess
		if result != nil {
			d.Stdout = result.Stdout
		}
	}
	if err := s.store.UpdateDeployment(ctx, d); err != nil {
		// Persistence failed after the deploy already happened — log the
		// underlying error context but keep the runner outcome as the
		// authoritative return so the caller still sees what actually went
		// wrong (or right) with the deploy itself.
		return d, fmt.Errorf("persist deployment outcome: %w (deploy err: %v)", err, runErr)
	}

	return d, runErr
}
