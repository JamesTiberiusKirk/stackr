package web

import (
	"context"

	hamrmw "github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/FyrmForge/hamr/pkg/storage"

	"github.com/jamestiberiuskirk/stackr/internal/jobs"
	"github.com/jamestiberiuskirk/stackr/internal/middleware"
	"github.com/jamestiberiuskirk/stackr/internal/realtime"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/service"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/auth/invite"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/auth/login"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/about"
	cronexec "github.com/jamestiberiuskirk/stackr/internal/web/handler/cron/executions"
	cronjobs "github.com/jamestiberiuskirk/stackr/internal/web/handler/cron/jobs"
	deploymentshandler "github.com/jamestiberiuskirk/stackr/internal/web/handler/deployments"
	envhandler "github.com/jamestiberiuskirk/stackr/internal/web/handler/env"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/home"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/routes"
	stackshandler "github.com/jamestiberiuskirk/stackr/internal/web/handler/stacks"
	"github.com/jamestiberiuskirk/stackr/internal/web/components"
)

// Deps holds the dependencies for route registration.
type Deps struct {
	Store          repo.Store
	BaseURL        string
	StaticBaseURL  string
	DevMode        bool
	SessionManager *auth.SessionManager
	AuthService    *service.AuthService
	InviteService  *service.InviteService
	FileStorage    storage.FileStorage
	Stackr         *service.StackrService
	Jobs           *jobs.Manager
	Hub            *realtime.Hub
}

// RegisterRoutes registers all web route handlers on the server.
func RegisterRoutes(srv *server.Server, deps *Deps) {
	e := srv.Echo()

	// Content Security Policy.
	csp := "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'"

	// Site routes.
	site := e.Group("")
	site.Use(middleware.Logging())
	site.Use(hamrmw.ErrorPages(components.ErrorPage))
	site.Use(hamrmw.SecureWithConfig(hamrmw.SecureConfig{
		ContentSecurityPolicy: csp,
	}))
	site.Use(hamrmw.FlashWithConfig(hamrmw.FlashConfig{Secure: !deps.DevMode}))
	site.Use(hamrmw.CSRFWithConfig(hamrmw.CSRFConfig{Secure: !deps.DevMode}))

	auth := hamrmw.NewBrowserAuth(deps.SessionManager,
		hamrmw.WithSubjectLoader(func(reqCtx context.Context, id string) (any, error) {
			return deps.Store.GetUserByID(reqCtx, id)
		}),
		hamrmw.WithLoginRedirect("/login"),
		hamrmw.WithHomeRedirect("/"),
	)
	site.Use(auth.Load())

	homeHandler := home.NewHandler(deps.Stackr)
	site.GET("/", homeHandler.Index, auth.RequireAuth())

	// Auth routes — login + logout. There is no public registration: users
	// are declared in stackr.yaml (synced on boot) and password is set via
	// `stackr set-password` or, in a later phase, an invite redeemed at
	// /accept-invite.
	loginHandler := login.NewHandler(deps.AuthService, deps.SessionManager)
	site.GET("/login", loginHandler.Page, auth.RequireNotAuth())
	site.POST("/login", loginHandler.Submit, auth.RequireNotAuth())
	site.POST("/login/validate/:field", loginHandler.FormRules.ValidationHandler("field"), auth.RequireNotAuth())
	site.POST("/logout", loginHandler.Logout, auth.RequireAuth())

	// Invite redemption — a logged-in user redeeming someone else's invite
	// would be confusing, so the routes are gated by RequireNotAuth.
	inviteHandler := invite.NewHandler(deps.Store, deps.InviteService)
	site.GET("/accept-invite", inviteHandler.Page, auth.RequireNotAuth())
	site.POST("/accept-invite", inviteHandler.Submit, auth.RequireNotAuth())

	// Read-slice pages — stacks detail, deployments, cron jobs/executions.
	// All require auth; mutations live in the future write slice.
	stacksH := stackshandler.NewHandler(deps.Stackr, deps.Jobs)
	site.GET("/stacks/:name", stacksH.Detail, auth.RequireAuth())
	site.POST("/stacks/bulk", stacksH.Bulk, auth.RequireAuth())
	site.POST("/stacks/:name/:action", stacksH.Action, auth.RequireAuth())

	deploymentsH := deploymentshandler.NewHandler(deps.Stackr)
	site.GET("/deployments", deploymentsH.Index, auth.RequireAuth())
	site.GET("/deployments/:id", deploymentsH.Detail, auth.RequireAuth())

	cronJobsH := cronjobs.NewHandler(deps.Stackr)
	site.GET("/cron", cronJobsH.Index, auth.RequireAuth())
	site.POST("/cron/jobs/:stack/:service/run", cronJobsH.RunNow, auth.RequireAuth())

	cronExecH := cronexec.NewHandler(deps.Stackr)
	site.GET("/cron/executions", cronExecH.Index, auth.RequireAuth())
	site.GET("/cron/executions/:id", cronExecH.Detail, auth.RequireAuth())

	routesH := routes.NewHandler(deps.Stackr)
	site.GET("/routes", routesH.Index, auth.RequireAuth())

	envH := envhandler.NewHandler(deps.Stackr)
	site.GET("/env", envH.Page, auth.RequireAuth())
	site.POST("/env", envH.Submit, auth.RequireAuth())
	site.POST("/env/:key/delete", envH.Delete, auth.RequireAuth())

	// Realtime websocket — auth-gated, but separate from the rest of the
	// CSRF/flash chain (no form, no flash) so we register it on the
	// Echo root and apply just the auth middleware. Clients subscribe
	// via ?topic=stack:nginx (repeatable) or after-connect JSON.
	if deps.Hub != nil {
		e.GET("/ws", realtime.Handler(deps.Hub), auth.Load(), auth.RequireAuth())
	}
}

// RegisterStaticPages registers handlers for static generation and runtime
// serving. Each call to StaticPage registers both a generation entry and a
// GET route. These handlers must not depend on database or session state.
func RegisterStaticPages(srv *server.Server) {
	h := about.NewHandler()
	srv.StaticPage("/about", h.About)
}
