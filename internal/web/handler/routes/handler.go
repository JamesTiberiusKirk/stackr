// Package routes serves /routes — a live view of every HTTP router
// traefik is currently serving. The data is fetched on each page load
// from traefik's API; the discoverer locates the API URL automatically
// (env override, stackr.yaml, then probes well-known URLs).
package routes

import (
	"net/http"

	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/service"
)

type handler struct {
	stackr *service.StackrService
}

// NewHandler creates a routes handler.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// GET /routes
//
// Three render branches:
//
//   - APIURL == "" → "Traefik not detected" stub. Most likely cause is
//     traefik isn't running or isn't on a reachable URL.
//   - APIURL != "" but err != nil → discovered an API but the request
//     failed (timeout, 5xx, JSON decode). Show the URL and the error so
//     operators can see exactly where to debug.
//   - Happy path → full table of routers.
func (h *handler) Index(c echo.Context) error {
	result, err := h.stackr.ListTraefikRouters(c.Request().Context())
	return respond.HTML(c, http.StatusOK, routesPage(c, result, err))
}
