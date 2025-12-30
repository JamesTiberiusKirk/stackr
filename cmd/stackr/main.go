package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/cronjobs"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

var (
	// Version is set at build time via -ldflags
	Version = "dev"
	// Commit is set at build time via -ldflags
	Commit = "unknown"
	// Date is set at build time via -ldflags
	Date = "unknown"
)

const helpMsg = `Stackr CLI - Docker Compose stack orchestration

Usage:
  stackr [flags] [stacks...]

Examples:
  stackr init
  stackr all update
  stackr mx5parts update --tag v1.0.3
  stackr stackr compose up --build
  stackr mx5parts vars-only -- env | grep STACK_STORAGE
  stackr monitoring get-vars
  stackr mystack run-cron backup
  stackr mystack run-cron backup -- /app/script.sh --verbose

Flags:
  -h, --help         Show this help message
  -v, --version      Show version information
  -D, --debug        Print debug messages
      --dry-run      Do not execute write actions; print docker compose config
      --tag <tag>    Update .env with image tag before deployment (requires update command)

Commands (can be combined):
  init           Initialize a new stackr project with config and example stacks
  all            Run on all stacks
  tear-down      Run "docker compose down" for the stack(s)
  update         Pull latest images and restart stack(s)
  backup         Back up config/volumes to BACKUP_DIR
  compose        Shorthand for "vars-only -- docker compose -f $DCFP <args...>"
  vars-only      Load env vars for the stack(s) and execute the command after --
  get-vars       Scan compose files for env vars and append missing entries to .env
  run-cron <svc> Manually execute a cron job service (optionally with custom command after --)
`

func main() {
	opts, showHelp, showVersion, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if showVersion {
		fmt.Printf("stackr version %s\n", Version)
		if Commit != "unknown" {
			fmt.Printf("commit: %s\n", Commit)
		}
		if Date != "unknown" {
			fmt.Printf("built: %s\n", Date)
		}
		return
	}

	if showHelp {
		fmt.Print(helpMsg)
		return
	}

	// Handle init command separately (doesn't need config)
	if opts.Init {
		if err := stackcmd.RunInit(); err != nil {
			log.Fatalf("init failed: %v", err)
		}
		return
	}

	repoRootOverride := strings.TrimSpace(os.Getenv("STACKR_REPO_ROOT"))

	// Handle run-cron command (needs config but bypasses normal stack manager)
	if opts.RunCron {
		if len(opts.Stacks) != 1 {
			log.Fatalf("run-cron requires exactly one stack name")
		}

		repoRoot, err := config.ResolveRepoRoot(repoRootOverride)
		if err != nil {
			log.Fatalf("failed to determine repo root: %v", err)
		}

		cfg, err := config.LoadForCLI(repoRoot)
		if err != nil {
			log.Fatalf("failed to load config: %v", err)
		}

		stack := opts.Stacks[0]
		service := opts.CronService
		customCmd := opts.VarsCommand

		if err := cronjobs.ExecuteJobManually(cfg, stack, service, customCmd); err != nil {
			log.Fatalf("failed to execute cron job: %v", err)
		}
		return
	}
	repoRoot, err := config.ResolveRepoRoot(repoRootOverride)
	if err != nil {
		log.Fatalf("failed to determine repo root: %v", err)
	}

	cfg, err := config.LoadForCLI(repoRoot)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	manager, err := stackcmd.NewManager(cfg)
	if err != nil {
		log.Fatalf("failed to initialize stack manager: %v", err)
	}

	if err := manager.Run(context.Background(), opts); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func parseArgs(args []string) (stackcmd.Options, bool, bool, error) {
	var opts stackcmd.Options
	var showVersion bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-h", "--help":
			return opts, true, false, nil
		case "-v", "--version":
			return opts, false, true, nil
		case "-D", "--debug":
			opts.Debug = true
		case "--dry-run":
			opts.DryRun = true
		case "--tag":
			if i+1 >= len(args) {
				return opts, false, false, fmt.Errorf("--tag requires a value")
			}
			i++
			opts.Tag = args[i]
		case "all":
			opts.All = true
		case "tear-down":
			opts.TearDown = true
		case "update":
			opts.Update = true
		case "backup":
			opts.Backup = true
		case "compose":
			opts.Compose = true
			opts.VarsOnly = true
			if i+1 < len(args) {
				opts.VarsCommand = append([]string{}, args[i+1:]...)
			}
			i = len(args)
		case "vars-only":
			opts.VarsOnly = true
		case "get-vars":
			opts.GetVars = true
		case "init":
			opts.Init = true
		case "run-cron":
			opts.RunCron = true
			if i+1 >= len(args) {
				return opts, false, false, fmt.Errorf("run-cron requires a service name")
			}
			i++
			opts.CronService = args[i]
			// Check for custom command after --
			if i+1 < len(args) && args[i+1] == "--" {
				i += 2 // Skip the -- separator
				if i < len(args) {
					opts.VarsCommand = append([]string{}, args[i:]...)
				}
				i = len(args)
			}
		case "--":
			opts.VarsOnly = true
			if i+1 < len(args) {
				opts.VarsCommand = append([]string{}, args[i+1:]...)
			}
			i = len(args)
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, false, false, fmt.Errorf("unknown flag %s", arg)
			}
			if !opts.All {
				opts.Stacks = append(opts.Stacks, arg)
			}
		}
	}

	return opts, false, showVersion, nil
}
