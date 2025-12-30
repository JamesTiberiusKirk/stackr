package cronjobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	cron "github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/runner"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

const (
	scheduleLabel    = "stackr.cron.schedule"
	runOnDeployLabel = "stackr.cron.run_on_deploy"
)

type Scheduler struct {
	mu   sync.Mutex
	cron *cron.Cron
	jobs []cronJob
	cfg  config.Config
}

type cronJob struct {
	Stack       string
	Service     string
	Schedule    string
	Profile     string
	RunOnDeploy bool
	ComposeFile string
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Labels   labelMap `yaml:"labels"`
	Profiles []string `yaml:"profiles"`
}

type labelMap map[string]string

func (l *labelMap) UnmarshalYAML(value *yaml.Node) error {
	result := make(map[string]string)
	if value == nil || value.Kind == 0 {
		*l = result
		return nil
	}

	switch value.Kind {
	case yaml.SequenceNode:
		for _, item := range value.Content {
			parts := strings.SplitN(item.Value, "=", 2)
			if len(parts) != 2 {
				continue
			}
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	case yaml.MappingNode:
		for i := 0; i < len(value.Content); i += 2 {
			key := strings.TrimSpace(value.Content[i].Value)
			if i+1 >= len(value.Content) {
				continue
			}
			result[key] = strings.TrimSpace(value.Content[i+1].Value)
		}
	default:
		return fmt.Errorf("unsupported labels format: %s", value.ShortTag())
	}

	*l = result
	return nil
}

func New(cfg config.Config) (*Scheduler, error) {
	jobs, err := discoverJobs(cfg)
	if err != nil {
		return nil, err
	}

	return &Scheduler{
		jobs: jobs,
		cfg:  cfg,
	}, nil
}

func (s *Scheduler) Start() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cron != nil {
		return nil
	}

	return s.startLocked()
}

func (s *Scheduler) Reload() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	jobs, err := discoverJobs(s.cfg)
	if err != nil {
		return err
	}

	if s.cron != nil {
		ctx := s.cron.Stop()
		<-ctx.Done()
		s.cron = nil
	}

	s.jobs = jobs
	return s.startLocked()
}

func (s *Scheduler) Stop() {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cron == nil {
		return
	}

	ctx := s.cron.Stop()
	<-ctx.Done()
	s.cron = nil
}

func (s *Scheduler) startLocked() error {
	if len(s.jobs) == 0 {
		log.Printf("no cron-enabled services detected")
		return nil
	}

	logger := cron.PrintfLogger(log.New(os.Stdout, "cron: ", log.LstdFlags))
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	c := cron.New(cron.WithParser(parser), cron.WithChain(cron.SkipIfStillRunning(logger)))

	for _, job := range s.jobs {
		jobCfg := job
		if _, err := parser.Parse(jobCfg.Schedule); err != nil {
			return fmt.Errorf("invalid cron schedule for stack=%s service=%s: %w", jobCfg.Stack, jobCfg.Service, err)
		}

		if _, err := c.AddFunc(jobCfg.Schedule, func() { s.execute(jobCfg) }); err != nil {
			return fmt.Errorf("failed to schedule cron job for stack=%s service=%s: %w", jobCfg.Stack, jobCfg.Service, err)
		}

		log.Printf("scheduled cron job stack=%s service=%s schedule=%q", jobCfg.Stack, jobCfg.Service, jobCfg.Schedule)

		if jobCfg.RunOnDeploy {
			go func(j cronJob) {
				log.Printf("run-on-deploy cron job triggered stack=%s service=%s", j.Stack, j.Service)
				s.execute(j)
			}(jobCfg)
		}
	}

	c.Start()
	s.cron = c

	// Run cleanup immediately on startup
	go func() {
		if err := CleanupOldContainers(s.cfg.Global.Cron.ContainerRetention); err != nil {
			log.Printf("cron container cleanup failed: %v", err)
		}
	}()

	// Schedule periodic cleanup (every 6 hours)
	if _, err := c.AddFunc("0 */6 * * *", func() {
		if err := CleanupOldContainers(s.cfg.Global.Cron.ContainerRetention); err != nil {
			log.Printf("cron container cleanup failed: %v", err)
		}
	}); err != nil {
		log.Printf("failed to schedule cleanup job: %v", err)
	}

	log.Printf("cron scheduler started with %d job(s)", len(s.jobs))
	return nil
}

// ExecuteJobManually finds and executes a specific cron job by stack and service name
// If customCmd is provided, it overrides the default command from the compose file
func ExecuteJobManually(cfg config.Config, stack, service string, customCmd []string) error {
	jobs, err := discoverJobs(cfg)
	if err != nil {
		return fmt.Errorf("failed to discover jobs: %w", err)
	}

	// Find the matching job
	var targetJob *cronJob
	for _, job := range jobs {
		if job.Stack == stack && job.Service == service {
			targetJob = &job
			break
		}
	}

	if targetJob == nil {
		return fmt.Errorf("cron job not found: stack=%s service=%s (make sure service has stackr.cron.schedule label)", stack, service)
	}

	// Create a temporary scheduler just to execute this one job
	s := &Scheduler{
		cfg: cfg,
	}

	if len(customCmd) > 0 {
		log.Printf("manually executing cron job with custom command: stack=%s service=%s cmd=%v", stack, service, customCmd)
	} else {
		log.Printf("manually executing cron job: stack=%s service=%s", stack, service)
	}
	s.executeWithCommand(*targetJob, customCmd)
	return nil
}

func discoverJobs(cfg config.Config) ([]cronJob, error) {
	entries, err := os.ReadDir(cfg.StacksDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read stacks dir %s: %w", cfg.StacksDir, err)
	}

	var jobs []cronJob
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		stackName := entry.Name()
		composePath := filepath.Join(cfg.StacksDir, stackName, "docker-compose.yml")
		content, err := os.ReadFile(composePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to read %s: %w", composePath, err)
		}

		var parsed composeFile
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", composePath, err)
		}

		for serviceName, service := range parsed.Services {
			schedule := strings.TrimSpace(service.Labels[scheduleLabel])
			if schedule == "" {
				continue
			}

			profile := ""
			if len(service.Profiles) == 1 {
				profile = strings.TrimSpace(service.Profiles[0])
			}

			runOnDeploy := false
			if raw := strings.TrimSpace(service.Labels[runOnDeployLabel]); raw != "" {
				parsed, err := strconv.ParseBool(raw)
				if err != nil {
					log.Printf("invalid %s value for stack=%s service=%s: %q", runOnDeployLabel, stackName, serviceName, raw)
				} else {
					runOnDeploy = parsed
				}
			}

			jobs = append(jobs, cronJob{
				Stack:       stackName,
				Service:     serviceName,
				Schedule:    schedule,
				Profile:     profile,
				RunOnDeploy: runOnDeploy,
				ComposeFile: composePath,
			})
		}
	}

	return jobs, nil
}

// executeWithCommand executes a cron job with an optional custom command
func (s *Scheduler) executeWithCommand(job cronJob, customCmd []string) {
	s.executeInternal(job, customCmd)
}

func (s *Scheduler) execute(job cronJob) {
	s.executeInternal(job, nil)
}

func (s *Scheduler) executeInternal(job cronJob, customCmd []string) {
	ctx, cancel := context.WithTimeout(context.Background(), runner.CommandTimeout)
	defer cancel()

	// Create separate log file writers for build and exec (if enabled)
	var logWriters *CronLogWriters
	if s.cfg.Global.Cron.EnableFileLogs {
		logsDir := filepath.Join(s.cfg.RepoRoot, s.cfg.Global.Cron.LogsDir)
		var err error
		logWriters, err = CreateCronLogWriters(logsDir, job.Stack, job.Service)
		if err != nil {
			log.Printf("failed to create log files for stack=%s service=%s: %v",
				job.Stack, job.Service, err)
			// Continue without file logging (fail gracefully)
			logWriters = nil
		}
	}
	defer func() {
		if logWriters != nil {
			_ = logWriters.Close()
		}
	}()

	// Write header to build log
	if logWriters != nil {
		_, _ = fmt.Fprintf(logWriters.BuildLog, "=== Cron Job Build/Pull Phase ===\n")
		_, _ = fmt.Fprintf(logWriters.BuildLog, "Stack: %s\n", job.Stack)
		_, _ = fmt.Fprintf(logWriters.BuildLog, "Service: %s\n", job.Service)
		_, _ = fmt.Fprintf(logWriters.BuildLog, "Time: %s\n", time.Now().Format(time.RFC3339))
		_, _ = fmt.Fprintf(logWriters.BuildLog, "==================================\n\n")
	}

	// Phase 1: Pull/build if needed
	if err := s.ensureImage(ctx, job, logWriters); err != nil {
		log.Printf("cron job image preparation failed stack=%s service=%s: %v",
			job.Stack, job.Service, err)
		return
	}

	// Write header to exec log
	if logWriters != nil {
		_, _ = fmt.Fprintf(logWriters.ExecLog, "=== Cron Job Execution ===\n")
		_, _ = fmt.Fprintf(logWriters.ExecLog, "Stack: %s\n", job.Stack)
		_, _ = fmt.Fprintf(logWriters.ExecLog, "Service: %s\n", job.Service)
		_, _ = fmt.Fprintf(logWriters.ExecLog, "Schedule: %s\n", job.Schedule)
		_, _ = fmt.Fprintf(logWriters.ExecLog, "Time: %s\n", time.Now().Format(time.RFC3339))
		_, _ = fmt.Fprintf(logWriters.ExecLog, "==========================\n\n")
	}

	// Phase 2: Execute the service
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	// Use MultiWriter to write to both buffer AND exec log file
	stdoutWriter := io.Writer(&stdout)
	stderrWriter := io.Writer(&stderr)
	if logWriters != nil {
		stdoutWriter = io.MultiWriter(&stdout, logWriters.ExecLog)
		stderrWriter = io.MultiWriter(&stderr, logWriters.ExecLog)
	}

	manager, err := stackcmd.NewManagerWithWriters(s.cfg, stdoutWriter, stderrWriter)
	if err != nil {
		log.Printf("cron job failed to create manager stack=%s service=%s: %v",
			job.Stack, job.Service, err)
		return
	}

	// Generate deterministic container name and REMOVE --rm flag
	containerName := GenerateContainerName(job.Stack, job.Service)
	composeArgs := []string{"docker", "compose", "--file", job.ComposeFile}
	if profile := strings.TrimSpace(job.Profile); profile != "" {
		composeArgs = append(composeArgs, "--profile", profile)
	}
	// CHANGED: Add --name flag, REMOVE --rm flag
	composeArgs = append(composeArgs, "run", "--name", containerName, job.Service)
	// Append custom command if provided
	if len(customCmd) > 0 {
		composeArgs = append(composeArgs, customCmd...)
	}

	opts := stackcmd.Options{
		Stacks:      []string{job.Stack},
		VarsOnly:    true,
		VarsCommand: composeArgs,
	}

	log.Printf("cron job started stack=%s service=%s container=%s",
		job.Stack, job.Service, containerName)

	if err := manager.Run(ctx, opts); err != nil {
		if logWriters != nil {
			_, _ = fmt.Fprintf(logWriters.ExecLog, "\n\n=== ERROR ===\n%s\n", stderr.String())
		}
		log.Printf("cron job failed stack=%s service=%s\nstdout: %s\nstderr: %s",
			job.Stack,
			job.Service,
			strings.TrimSpace(stdout.String()),
			strings.TrimSpace(stderr.String()),
		)
		return
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		log.Printf("cron job finished stack=%s service=%s", job.Stack, job.Service)
		return
	}

	log.Printf("cron job finished stack=%s service=%s output=%s",
		job.Stack, job.Service, output)
}

// ensureImage runs docker compose pull to ensure image is available
// Logs output to build log file
func (s *Scheduler) ensureImage(ctx context.Context, job cronJob, logWriters *CronLogWriters) error {
	pullArgs := []string{"docker", "compose", "--file", job.ComposeFile}
	if profile := strings.TrimSpace(job.Profile); profile != "" {
		pullArgs = append(pullArgs, "--profile", profile)
	}
	pullArgs = append(pullArgs, "pull", "--quiet", job.Service)

	var stdout, stderr bytes.Buffer
	buildStdout := io.Writer(&stdout)
	buildStderr := io.Writer(&stderr)

	if logWriters != nil {
		buildStdout = io.MultiWriter(&stdout, logWriters.BuildLog)
		buildStderr = io.MultiWriter(&stderr, logWriters.BuildLog)
	}

	cmd := exec.CommandContext(ctx, pullArgs[0], pullArgs[1:]...)
	cmd.Stdout = buildStdout
	cmd.Stderr = buildStderr

	if err := cmd.Run(); err != nil {
		// Pull might fail if image is built locally, that's OK
		if logWriters != nil {
			_, _ = fmt.Fprintf(logWriters.BuildLog, "Note: Pull failed (image may be built locally)\n")
		}
	}

	if logWriters != nil && stdout.Len() == 0 && stderr.Len() == 0 {
		_, _ = fmt.Fprintf(logWriters.BuildLog, "Image already up to date (no pull needed)\n")
	}

	return nil
}
