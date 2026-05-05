package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/FyrmForge/hamr/pkg/config"
	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/FyrmForge/hamr/pkg/storage"
	stackrcfg "github.com/jamestiberiuskirk/stackr/internal/config"
	"github.com/jamestiberiuskirk/stackr/internal/api"
	"github.com/jamestiberiuskirk/stackr/internal/background"
	appdb "github.com/jamestiberiuskirk/stackr/internal/db"
	"github.com/jamestiberiuskirk/stackr/internal/dockerwatch"
	"github.com/jamestiberiuskirk/stackr/internal/jobs"
	"github.com/jamestiberiuskirk/stackr/internal/realtime"
	"github.com/jamestiberiuskirk/stackr/internal/reconcile"
	"github.com/jamestiberiuskirk/stackr/internal/repo/sqlite"
	"github.com/jamestiberiuskirk/stackr/internal/service"
	"github.com/jamestiberiuskirk/stackr/internal/web"
	"github.com/jamestiberiuskirk/stackr/internal/web/components"

	dockerclient "github.com/docker/docker/client"
)

// version is set at build time via ldflags.
var version = "dev"

var (
	envPort          = config.GetEnvOrDefaultInt("PORT", 8080)
	// DEV_MODE defaults to false (fail closed in prod). Local dev sets
	// DEV_MODE=true via .env so the scaffolded `.env` ships with it set
	// explicitly. This makes the STRIPE_MOCK production guard actually
	// guard — a leftover STRIPE_MOCK=true in a prod deploy without
	// DEV_MODE explicitly set would otherwise slip through.
	envDevMode       = config.GetEnvOrDefaultBool("DEV_MODE", false)
	envBaseURL       = config.GetEnvOrDefault("BASE_URL", "")
	envDatabasePath  = config.GetEnvOrDefault("DATABASE_PATH", "./data/daemon.db")
	envStaticBaseURL = config.GetEnvOrDefault("STATIC_BASE_URL", "/static")
	envStoragePath   = config.GetEnvOrDefault("STORAGE_PATH", "./uploads")
)

func main() {
	generateFlag := flag.Bool("generate", false, "generate static pages and exit")
	flag.Parse()

	log := logging.New(!envDevMode)
	slog.SetDefault(log)

	components.StaticBaseURL = envStaticBaseURL

	// Base URL (cookie domain & CORS).
	baseOrigin, baseDomain, err := config.ParseBaseURL(envBaseURL)
	if err != nil {
		log.Error("invalid BASE_URL", "error", err)
		os.Exit(1)
	}
	components.BaseURL = baseOrigin

	// Server.
	srv, err := server.New(
		server.WithPort(envPort),
		server.WithDevMode(envDevMode),
		server.WithStaticDir("static"),
		server.WithStaticDistDir("dist"),
		server.WithGeneratedDir("generated"),
	)
	if err != nil {
		log.Error("failed to create server", "error", err)
		os.Exit(1)
	}

	if baseOrigin != "" {
		srv.Echo().Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins:     []string{baseOrigin},
			AllowCredentials: true,
		}))
	}

	// Static page generation — no heavy deps needed.
	web.RegisterStaticPages(srv)
	if *generateFlag {
		if err := srv.GenerateStatic("generated"); err != nil {
			log.Error("generate static pages failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Database.
	connectCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	database, err := appdb.ConnectContext(connectCtx, envDatabasePath)
	cancel()
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}

	// Run migrations at startup.
	if err := appdb.AutoMigrate(database); err != nil {
		log.Error("auto-migration failed", "error", err)
		os.Exit(1)
	}
	log.Info("auto-migration completed")
	store := sqlite.NewStore(database)

	// Sessions.
	sessionManager := auth.NewSessionManager(store,
		auth.WithCookieSecure(!envDevMode),
		auth.WithCookieDomain(baseDomain),
	)

	// Auth service.
	authService := service.NewAuthService(store)
	inviteService := service.NewInviteService(store)

	// File storage (local).
	fileStorage, err := storage.NewLocalStorage(envStoragePath)
	if err != nil {
		log.Error("failed to init storage", "error", err)
		os.Exit(1)
	}

	// Stackr config — declares the bearer token, stacks dir, env file, and
	// global stackr settings. STACKR_REPO_ROOT picks the parent repo (defaults
	// to cwd). The daemon refuses to boot without a valid config since every
	// authed API route depends on it.
	repoRoot, err := stackrcfg.ResolveRepoRoot(os.Getenv("STACKR_REPO_ROOT"))
	if err != nil {
		log.Error("failed to resolve stackr repo root", "error", err)
		os.Exit(1)
	}
	stackrCfg, err := stackrcfg.Load(repoRoot)
	if err != nil {
		log.Error("failed to load stackr config", "error", err, "repoRoot", repoRoot)
		os.Exit(1)
	}
	log.Info("loaded stackr config",
		"repoRoot", stackrCfg.RepoRoot,
		"stacksDir", stackrCfg.StacksDir,
		"envFile", stackrCfg.EnvFile,
	)
	stackrService := service.NewStackrService(stackrCfg, store)

	// Realtime hub + jobs manager — the daemon's pub/sub spine for live
	// UI updates. The hub satisfies jobs.Publisher so every job state
	// transition fans out to topic-based WS subscribers.
	hub := realtime.New()
	jobMgr := jobs.New(hub)

	// User sync — reconcile the `users` table to `auth.users` declared in
	// stackr.yaml. Plan + Apply are split so a future approval-mode boot
	// can persist the plan and wait for an admin instead of applying.
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 30*time.Second)
	userPlan, err := service.PlanUserSync(syncCtx, store, stackrCfg.Global.Auth.Users)
	if err != nil {
		syncCancel()
		log.Error("user sync plan failed", "error", err)
		os.Exit(1)
	}
	if !userPlan.IsEmpty() {
		log.Info("applying user sync plan",
			"create", len(userPlan.Create),
			"update", len(userPlan.Update),
			"delete", len(userPlan.Delete),
		)
	}
	if err := service.ApplyUserSync(syncCtx, store, userPlan); err != nil {
		syncCancel()
		log.Error("user sync apply failed", "error", err)
		os.Exit(1)
	}
	syncCancel()

	api.RegisterRoutes(srv, &api.Deps{
		Store:  store,
		Stackr: stackrService,
	})

	web.RegisterRoutes(srv, &web.Deps{
		Store:          store,
		BaseURL:        baseOrigin,
		StaticBaseURL:  envStaticBaseURL,
		DevMode:        envDevMode,
		SessionManager: sessionManager,
		AuthService:    authService,
		InviteService:  inviteService,
		FileStorage:    fileStorage,
		Stackr:         stackrService,
		Jobs:           jobMgr,
		Hub:            hub,
	})

	// Background services: cron scheduler + filesystem watcher + removal
	// cleanup. Started before the HTTP server so the first incoming request
	// finds the daemon fully wired. Failures are logged but non-fatal — the
	// HTTP API is the daemon's primary contract and stays up regardless.
	bg, err := background.New(stackrCfg, store, log)
	if err != nil {
		log.Error("failed to init background services", "error", err)
		os.Exit(1)
	}
	if err := bg.Start(context.Background()); err != nil {
		log.Warn("background services degraded", "error", err)
	}

	// File watcher → auto-reconciler. Stack file changes (compose,
	// support files) enqueue an "up" job for the affected stack.
	// Plan/Apply seam in internal/reconcile means a future approval
	// mode can wedge between detection and apply without touching the
	// watcher.
	enqueueAction := func(stack, action, trigger string) string {
		kind := "stack." + action
		return jobMgr.Enqueue(kind, stack, func(ctx context.Context) error {
			_, err := stackrService.PerformAction(ctx, stack, action, trigger)
			return err
		})
	}
	rec := reconcile.New(stackrCfg, enqueueAction)
	reconcileCtx, reconcileCancel := context.WithCancel(context.Background())
	go func() {
		if err := rec.Run(reconcileCtx); err != nil {
			log.Warn("reconcile watcher exited", "error", err)
		}
	}()

	// Docker /events listener — translates container start/die into
	// stack.status broadcasts on the realtime hub. Best-effort: if the
	// docker client can't connect, the daemon stays up and just doesn't
	// push live status (operators can still read the page; pills are
	// just stale-on-load).
	dockerCli, dockerErr := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	// Explicit cancel below at shutdown; we don't `defer` because the
	// other os.Exit error paths in this function would skip the defer
	// (gocritic: exitAfterDefer).
	dockerWatchCtx, dockerWatchCancel := context.WithCancel(context.Background())
	if dockerErr != nil {
		log.Warn("docker client init failed; live status disabled", "error", dockerErr)
	} else {
		projectMap, mapErr := dockerwatch.BuildProjectMap(stackrCfg)
		if mapErr != nil {
			log.Warn("docker watcher project-map init failed", "error", mapErr)
		} else {
			watcher := dockerwatch.New(dockerCli, hub, stackrService, projectMap)
			go watcher.Run(dockerWatchCtx)
			log.Info("docker watcher running", "stacks", len(projectMap))
		}
	}

	log.Info("starting server", "version", version, "port", envPort, "devMode", envDevMode)
	srvErr := srv.Start()

	// srv.Start blocks until SIGINT/SIGTERM (or a listener error). Tear down
	// background services before logging/exiting so cron and watch don't keep
	// running while the HTTP socket is gone.
	dockerWatchCancel()
	reconcileCancel()
	bg.Stop()

	if srvErr != nil {
		log.Error("server stopped", "error", srvErr)
		os.Exit(1)
	}
}
