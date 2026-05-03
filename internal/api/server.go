package api

import (
	"github.com/FyrmForge/hamr/pkg/server"

	"github.com/jamestiberiuskirk/stackr/internal/api/handler/deploy"
	"github.com/jamestiberiuskirk/stackr/internal/api/handler/deployments"
	"github.com/jamestiberiuskirk/stackr/internal/api/handler/health"
	"github.com/jamestiberiuskirk/stackr/internal/api/handler/stacks"
	"github.com/jamestiberiuskirk/stackr/internal/middleware"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/service"
)

// Deps holds the dependencies for API route registration.
type Deps struct {
	Store  repo.Store
	Stackr *service.StackrService
}

// RegisterRoutes registers all API route handlers on the server.
//
// /api/health is unauthenticated (used by uptime monitors). All other routes
// sit under a Bearer-authed group keyed off the stackr config token, matching
// the legacy daemon's auth shape so existing CLI clients can talk to the new
// daemon unchanged.
func RegisterRoutes(srv *server.Server, deps *Deps) {
	api := srv.Echo().Group("/api")
	api.Use(middleware.Logging())

	healthHandler := health.NewHandler(deps.Store)
	api.GET("/health", healthHandler.Health)

	authed := api.Group("", middleware.BearerAuth(deps.Stackr.Token()))

	stacksHandler := stacks.NewHandler(deps.Stackr)
	authed.GET("/stacks", stacksHandler.List)
	authed.GET("/stacks/:name", stacksHandler.Get)

	deploymentsHandler := deployments.NewHandler(deps.Store)
	authed.GET("/deployments", deploymentsHandler.List)
	authed.GET("/deployments/:id", deploymentsHandler.Get)

	deployHandler := deploy.NewHandler(deps.Stackr)
	authed.POST("/deploy", deployHandler.Submit)
}
