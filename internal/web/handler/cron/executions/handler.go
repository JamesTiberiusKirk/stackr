// Package executions serves the cron-executions read views: a list at
// /cron/executions and a per-id detail at /cron/executions/{id} showing
// stdout / error / timing for a single run.
package executions

import (
	"net/http"

	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/service"
)

// listLimit caps the index view. Pagination isn't wired yet — at current
// scale (a single homelab) the latest 100 runs is plenty; the API can
// continue paging when phase-2-real lands.
const listLimit = 100

// handler renders /cron/executions and /cron/executions/{id}.
type handler struct {
	stackr *service.StackrService
}

// NewHandler creates a cron-executions handler.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// GET /cron/executions
//
// Optional ?stack= and ?service= query params filter the list — handy on
// the stack-detail page which links here with stack pre-selected.
func (h *handler) Index(c echo.Context) error {
	filter := repo.CronExecutionFilter{
		Stack:   c.QueryParam("stack"),
		Service: c.QueryParam("service"),
		Limit:   listLimit,
	}
	rows, err := h.stackr.ListCronExecutions(c.Request().Context(), filter)
	if err != nil {
		return respond.HTML(c, http.StatusOK, executionsPage(c, nil, filter, err))
	}
	return respond.HTML(c, http.StatusOK, executionsPage(c, rows, filter, nil))
}

// GET /cron/executions/:id
func (h *handler) Detail(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing execution id")
	}
	row, err := h.stackr.GetCronExecution(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if row == nil {
		return echo.NewHTTPError(http.StatusNotFound, "execution not found")
	}
	return respond.HTML(c, http.StatusOK, executionDetailPage(c, row))
}
