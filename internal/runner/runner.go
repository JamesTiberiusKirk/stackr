package runner

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/envfile"
	"github.com/jamestiberiuskirk/stackr/internal/remote"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

const CommandTimeout = 15 * time.Minute

type Result struct {
	Status string `json:"status"`
	Stack  string `json:"stack"`
	Tag    string `json:"tag"`
	Stdout string `json:"stdout,omitempty"`
}

type CommandError struct {
	Msg    string
	Stdout string
	Stderr string
	Code   int
}

func (e *CommandError) Error() string {
	return e.Msg
}

type Runner struct {
	cfg config.Config
	mu  sync.Mutex
}

func New(cfg config.Config) *Runner {
	return &Runner{cfg: cfg}
}

func parseDeployArgs(args []string) stackcmd.Options {
	opts := stackcmd.Options{}
	for _, arg := range args {
		switch arg {
		case "update":
			opts.Update = true
		case "tear-down":
			opts.TearDown = true
		case "backup":
			opts.Backup = true
		case "vars-only":
			opts.VarsOnly = true
		case "get-vars":
			opts.GetVars = true
		case "all":
			opts.All = true
		}
	}
	return opts
}

func (r *Runner) Deploy(ctx context.Context, stack string, stackCfg config.StackConfig, tag string) (*Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	log.Printf("starting deployment: stack=%s tag=%s tagEnv=%s args=%v", stack, tag, stackCfg.TagEnv, stackCfg.Args)
	log.Printf("config: RepoRoot=%s HostRepoRoot=%s StacksDir=%s", r.cfg.RepoRoot, r.cfg.HostRepoRoot, r.cfg.StacksDir)

	snap, err := envfile.SnapshotFile(r.cfg.EnvFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read env file: %w", err)
	}

	previous, err := envfile.Update(r.cfg.EnvFile, stackCfg.TagEnv, tag)
	if err != nil {
		return nil, fmt.Errorf("failed to update env file: %w", err)
	}

	log.Printf("updated %s to %s (previous: %s)", stackCfg.TagEnv, tag, previous)

	// Check if remote stack and sync before deployment
	stackInfo, err := stackcmd.ResolveStackPath(r.cfg, stack)
	if err != nil {
		if rollbackErr := envfile.Restore(r.cfg.EnvFile, snap); rollbackErr != nil {
			log.Printf("failed to roll back %s after stack resolution error: %v", stackCfg.TagEnv, rollbackErr)
		}
		return nil, fmt.Errorf("failed to resolve stack: %w", err)
	}

	if stackInfo.Type == stackcmd.StackTypeRemote {
		// Read current .env for variable resolution
		envVals, _, err := readEnvFile(r.cfg.EnvFile)
		if err != nil {
			if rollbackErr := envfile.Restore(r.cfg.EnvFile, snap); rollbackErr != nil {
				log.Printf("failed to roll back %s after env read error: %v", stackCfg.TagEnv, rollbackErr)
			}
			return nil, fmt.Errorf("failed to read env file: %w", err)
		}

		remoteMgr := remote.NewManager(r.cfg)
		if err := remoteMgr.EnsureRemoteStack(context.Background(), stack, envVals); err != nil {
			// Use cached version on git failure (graceful degradation)
			log.Printf("warning: git sync failed for %s, using cached version: %v", stack, err)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	manager, err := stackcmd.NewManagerWithWriters(r.cfg, &stdout, &stderr)
	if err != nil {
		if rollbackErr := envfile.Restore(r.cfg.EnvFile, snap); rollbackErr != nil {
			log.Printf("failed to roll back %s after manager creation error: %v", stackCfg.TagEnv, rollbackErr)
		}
		return nil, fmt.Errorf("failed to create stack manager: %w", err)
	}

	opts := parseDeployArgs(stackCfg.Args)
	opts.Stacks = []string{stack}

	if err := manager.Run(ctx, opts); err != nil {
		// Log the full error details
		log.Printf("deployment failed for stack=%s: %v", stack, err)
		log.Printf("deployment stdout:\n%s", stdout.String())
		log.Printf("deployment stderr:\n%s", stderr.String())

		if rollbackErr := envfile.Restore(r.cfg.EnvFile, snap); rollbackErr != nil {
			log.Printf("failed to roll back %s after deploy error: %v", stackCfg.TagEnv, rollbackErr)
		} else {
			log.Printf("rolled back %s to previous value after deploy failure", stackCfg.TagEnv)
		}

		return nil, &CommandError{
			Msg:    fmt.Sprintf("deployment failed for stack=%s", stack),
			Code:   1,
			Stdout: stdout.String(),
			Stderr: stderr.String(),
		}
	}

	log.Printf("deployment finished for stack=%s tag=%s", stack, tag)

	return &Result{
		Status: "ok",
		Stack:  stack,
		Tag:    tag,
		Stdout: strings.TrimSpace(stdout.String()),
	}, nil
}

// readEnvFile reads and parses the env file
func readEnvFile(path string) (map[string]string, string, error) {
	content, err := envfile.SnapshotFile(path)
	if err != nil {
		return nil, "", err
	}

	data := string(content.Data)

	// Parse env file content into map
	lines := strings.Split(data, "\n")
	envVals := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			envVals[parts[0]] = parts[1]
		}
	}

	return envVals, data, nil
}
