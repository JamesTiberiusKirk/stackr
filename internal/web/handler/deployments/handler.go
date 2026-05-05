// Package deployments serves /deployments and /deployments/{id} —
// deployment history list + per-deployment detail (stdout, error, timing).
package deployments

import (
	"net/http"

	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/service"
)

const listLimit = 100

type handler struct {
	stackr *service.StackrService
}

// NewHandler creates a deployments handler.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// GET /deployments
//
// Optional ?stack= filters to a single stack — used by the stack-detail
// page's "Recent deployments" link.
func (h *handler) Index(c echo.Context) error {
	filter := repo.DeploymentFilter{
		Stack: c.QueryParam("stack"),
		Limit: listLimit,
	}
	rows, err := h.stackr.ListDeployments(c.Request().Context(), filter)
	if err != nil {
		return respond.HTML(c, http.StatusOK, deploymentsPage(c, nil, filter, err))
	}
	return respond.HTML(c, http.StatusOK, deploymentsPage(c, rows, filter, nil))
}

// GET /deployments/:id
func (h *handler) Detail(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing deployment id")
	}
	row, err := h.stackr.GetDeployment(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if row == nil {
		return echo.NewHTTPError(http.StatusNotFound, "deployment not found")
	}
	return respond.HTML(c, http.StatusOK, deploymentDetailPage(c, row))
}
