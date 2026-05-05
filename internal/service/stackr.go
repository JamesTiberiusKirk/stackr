package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jamestiberiuskirk/stackr/internal/compose"
	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/cron"
	"github.com/jamestiberiuskirk/stackr/internal/cronjobs"
	"github.com/jamestiberiuskirk/stackr/internal/runner"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
	"github.com/jamestiberiuskirk/stackr/internal/traefik"

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
	cfg              config.Config
	store            repo.Store
	runner           *runner.Runner
	traefikDiscover  *traefik.Discoverer
}

// NewStackrService builds a StackrService from a loaded stackr config and the
// repo store (used for deployment / event lookups keyed by stack name).
func NewStackrService(cfg config.Config, store repo.Store) *StackrService {
	return &StackrService{
		cfg:             cfg,
		store:           store,
		runner:          runner.New(cfg),
		traefikDiscover: traefik.NewDiscoverer(cfg.Global.Traefik.APIURL),
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

// TraefikRouters describes the result of a /routes page query. APIURL is
// empty when the discoverer didn't find a reachable traefik — handlers
// distinguish that from "found, but listed nothing" (an active traefik
// with zero routers).
type TraefikRouters struct {
	APIURL  string
	Routers []traefik.Router
}

// ListTraefikRouters resolves the traefik API URL via the discoverer and
// fetches every HTTP router. APIURL == "" in the result means no traefik
// was detected — the page renders a "Traefik not detected" stub instead
// of an error.
func (s *StackrService) ListTraefikRouters(ctx context.Context) (TraefikRouters, error) {
	url := s.traefikDiscover.Resolve(ctx)
	if url == "" {
		return TraefikRouters{}, nil
	}
	client := traefik.NewClient(url)
	routers, err := client.ListRouters(ctx)
	if err != nil {
		return TraefikRouters{APIURL: url}, fmt.Errorf("list routers: %w", err)
	}
	return TraefikRouters{APIURL: url, Routers: routers}, nil
}

// InspectStack returns a structured read-only view of a stack's compose
// file (services, ports, traefik routes). Pure declarative inspection —
// runtime state (whether the container is actually running, whether the
// port is bound) is intentionally not included.
//
// The stack must already pass GetStack — invalid names and missing
// directories surface as the same typed sentinels.
func (s *StackrService) InspectStack(ctx context.Context, name string) (compose.Inspection, error) {
	info, err := s.GetStack(ctx, name)
	if err != nil {
		return compose.Inspection{}, err
	}
	composePath := info.PrimaryComposePath()
	if composePath == "" {
		return compose.Inspection{}, fmt.Errorf("stack %q has no compose file", name)
	}
	insp, err := compose.Inspect(composePath)
	if err != nil {
		return compose.Inspection{}, fmt.Errorf("inspect stack %q: %w", name, err)
	}
	return insp, nil
}

// ListCronJobs returns all cron jobs discovered from compose labels, in
// stable (stack, service) order. Pure read against the filesystem — no
// scheduler state involved.
func (s *StackrService) ListCronJobs(_ context.Context) ([]cronjobs.JobInfo, error) {
	jobs, err := cronjobs.DiscoverJobs(s.cfg)
	if err != nil {
		return nil, fmt.Errorf("discover cron jobs: %w", err)
	}
	return jobs, nil
}

// RunCronJob triggers a one-off run of a discovered cron job. The
// execution is recorded to the same `cron_executions` table the
// scheduler writes to, so manual and automatic runs share /cron/executions.
//
// Note this is synchronous — the HTTP request blocks until the cron
// container exits. At homelab scale that's fine; if jobs ever take
// minutes a future async-trigger + status-poll pattern would replace it.
func (s *StackrService) RunCronJob(_ context.Context, stack, service string) error {
	if err := stackcmd.ValidateStackName(stack); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidStackName, err)
	}
	rec := cron.NewRecorder(s.store)
	if err := cronjobs.ExecuteJobManually(s.cfg, stack, service, nil, rec); err != nil {
		return fmt.Errorf("execute cron job: %w", err)
	}
	return nil
}

// ListCronExecutions returns persisted cron execution rows matching filter.
// A nil filter means "everything"; the typed zero value is also fine.
func (s *StackrService) ListCronExecutions(ctx context.Context, filter repo.CronExecutionFilter) ([]repo.CronExecution, error) {
	rows, err := s.store.ListCronExecutions(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list cron executions: %w", err)
	}
	return rows, nil
}

// GetCronExecution returns a single execution row by id, or nil if not
// found. Handlers translate nil → 404.
func (s *StackrService) GetCronExecution(ctx context.Context, id string) (*repo.CronExecution, error) {
	row, err := s.store.GetCronExecutionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get cron execution: %w", err)
	}
	return row, nil
}

// ListDeployments returns persisted deployment rows matching filter.
func (s *StackrService) ListDeployments(ctx context.Context, filter repo.DeploymentFilter) ([]repo.Deployment, error) {
	rows, err := s.store.ListDeployments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	return rows, nil
}

// GetDeployment returns a single deployment row by id, or nil if not found.
func (s *StackrService) GetDeployment(ctx context.Context, id string) (*repo.Deployment, error) {
	row, err := s.store.GetDeploymentByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	return row, nil
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
