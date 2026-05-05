package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/jamestiberiuskirk/stackr/internal/compose"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

// Container state values reported via `docker compose ps`.
const (
	ServiceStateRunning    = "running"
	ServiceStateExited     = "exited"
	ServiceStateCreated    = "created"
	ServiceStateRestarting = "restarting"
	ServiceStatePaused     = "paused"
	ServiceStateDead       = "dead"
	// ServiceStateMissing means the service is declared in the compose
	// file but has no container — never been brought up, or torn down.
	ServiceStateMissing = "missing"
)

// Aggregate values describe a stack as a whole. Exposed so the UI can
// switch on a single string instead of recomputing the rule.
const (
	StackAggregateEmpty   = "empty"
	StackAggregateRunning = "running"
	StackAggregatePartial = "partial"
	StackAggregateStopped = "stopped"
)

// StackStatus is the live state of a single stack: what the compose file
// declares, joined with what's actually running according to Docker.
type StackStatus struct {
	Stack    string
	Services []ServiceStatus
}

// ServiceStatus is one declared service's runtime state. State == "missing"
// means the service is in the compose file but has no container at all.
type ServiceStatus struct {
	Name      string // declared service name from compose
	Container string // actual container name (empty if missing)
	State     string // ServiceState* constant
	Status    string // human-readable status string from docker (e.g. "Up 2 minutes")
	Image     string
}

// IsRunning returns true for the single state we treat as "healthy and
// up". Restarting / paused / created don't count — they're observable
// but not what the operator wants.
func (s ServiceStatus) IsRunning() bool {
	return s.State == ServiceStateRunning
}

// RunningCount is the number of declared services currently in the
// "running" state.
func (s StackStatus) RunningCount() int {
	n := 0
	for _, sv := range s.Services {
		if sv.IsRunning() {
			n++
		}
	}
	return n
}

// TotalCount is the number of declared services — same as len(Services),
// kept as a method so templ doesn't need a Go-expression block.
func (s StackStatus) TotalCount() int {
	return len(s.Services)
}

// Aggregate collapses per-service state into one bucket. Used by the
// stacks index to colour the row at a glance.
func (s StackStatus) Aggregate() string {
	total := s.TotalCount()
	if total == 0 {
		return StackAggregateEmpty
	}
	running := s.RunningCount()
	switch running {
	case 0:
		return StackAggregateStopped
	case total:
		return StackAggregateRunning
	default:
		return StackAggregatePartial
	}
}

// composePsLine is the docker compose ps --format=json line shape we
// care about. Newer compose emits JSONL (one object per line); older
// versions emit a single JSON array. parseComposePs handles both.
type composePsLine struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Status  string `json:"Status"`
	Image   string `json:"Image"`
}

// StackStatus returns the live state of the named stack. It runs
// `docker compose ps` against the resolved compose files and merges the
// result with the declared services from the compose file. A service
// with no container surfaces as State="missing" rather than being
// dropped — operators need to see "this is supposed to be here".
func (s *StackrService) StackStatus(ctx context.Context, name string) (StackStatus, error) {
	if _, err := s.GetStack(ctx, name); err != nil {
		return StackStatus{}, err
	}
	info, err := stackcmd.ResolveStackPath(s.cfg, name)
	if err != nil {
		return StackStatus{}, fmt.Errorf("resolve stack path: %w", err)
	}
	inspection, err := compose.Inspect(info.PrimaryComposePath())
	if err != nil {
		return StackStatus{}, fmt.Errorf("inspect compose: %w", err)
	}

	containers, err := dockerComposePs(ctx, s.cfg.RepoRoot, info.ComposePaths)
	if err != nil {
		return StackStatus{}, err
	}

	byService := make(map[string]composePsLine, len(containers))
	for _, c := range containers {
		byService[c.Service] = c
	}

	out := StackStatus{Stack: name, Services: make([]ServiceStatus, 0, len(inspection.Services))}
	for _, declared := range inspection.Services {
		ss := ServiceStatus{Name: declared.Name, Image: declared.Image, State: ServiceStateMissing}
		if c, ok := byService[declared.Name]; ok {
			ss.Container = c.Name
			ss.State = c.State
			ss.Status = c.Status
			if c.Image != "" {
				ss.Image = c.Image
			}
		}
		out.Services = append(out.Services, ss)
	}
	return out, nil
}

// AllStackStatuses returns one StackStatus per discovered stack. Used
// by the index — failures on a single stack degrade to an empty
// Services slice for that row, never block the page.
func (s *StackrService) AllStackStatuses(ctx context.Context) (map[string]StackStatus, error) {
	stacks, err := s.ListStacks(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]StackStatus, len(stacks))
	for _, st := range stacks {
		status, err := s.StackStatus(ctx, st.Name)
		if err != nil {
			out[st.Name] = StackStatus{Stack: st.Name}
			continue
		}
		out[st.Name] = status
	}
	return out, nil
}

// dockerComposePs runs the underlying `docker compose ps --format=json`
// and returns parsed lines. Working directory is the stackr repo root
// so relative compose paths and bind-mount sources resolve correctly.
func dockerComposePs(ctx context.Context, repoRoot string, composePaths []string) ([]composePsLine, error) {
	args := []string{"compose"}
	for _, p := range composePaths {
		args = append(args, "-f", p)
	}
	args = append(args, "ps", "-a", "--format=json")

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = repoRoot
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker compose ps: %w (stderr: %s)", err, stderr.String())
	}
	return parseComposePs(stdout.Bytes())
}

// parseComposePs handles both JSONL (newer compose) and JSON-array
// (older compose) outputs. Empty input is a valid empty result —
// "no containers" is normal for a stack that's never been brought up.
func parseComposePs(out []byte) ([]composePsLine, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var lines []composePsLine
		if err := json.Unmarshal(trimmed, &lines); err != nil {
			return nil, fmt.Errorf("decode ps array: %w", err)
		}
		return lines, nil
	}

	var lines []composePsLine
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry composePsLine
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("decode ps line: %w", err)
		}
		lines = append(lines, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan ps output: %w", err)
	}
	return lines, nil
}
