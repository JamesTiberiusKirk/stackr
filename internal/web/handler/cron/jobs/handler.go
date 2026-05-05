// Package jobs serves the cron-jobs view at /cron — a list of every
// service across the configured stacks that carries a cron schedule
// label, plus a per-row "Run now" button that triggers a one-off
// manual execution recorded into /cron/executions.
package jobs

import (
	"net/http"

	hamrmw "github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/service"
)

// handler renders /cron. The list is rebuilt on every request — discovery
// reads the stacks dir, which is acceptable for a small repo and avoids a
// stale cache when an operator edits compose files.
type handler struct {
	stackr *service.StackrService
}

// NewHandler creates a new cron-jobs handler.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// GET /cron
func (h *handler) Index(c echo.Context) error {
	jobs, err := h.stackr.ListCronJobs(c.Request().Context())
	if err != nil {
		// Surface to the page rather than 500 — the cron view degrading
		// shouldn't take down the whole UI when one compose file is malformed.
		return respond.HTML(c, http.StatusOK, jobsPage(c, nil, err))
	}
	return respond.HTML(c, http.StatusOK, jobsPage(c, jobs, nil))
}

// POST /cron/jobs/:stack/:service/run
//
// Triggers a one-off execution and redirects to /cron/executions with a
// stack filter so the operator sees the freshly-recorded row at the top.
// Synchronous — page blocks until the cron container exits. Long-running
// jobs would benefit from an async pattern; defer.
func (h *handler) RunNow(c echo.Context) error {
	stack := c.Param("stack")
	svc := c.Param("service")
	if stack == "" || svc == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing stack or service")
	}
	if err := h.stackr.RunCronJob(c.Request().Context(), stack, svc); err != nil {
		hamrmw.SetFlash(c, "Run failed for "+stack+"/"+svc+": "+err.Error(), hamrmw.FlashError)
	} else {
		hamrmw.SetFlash(c, "Triggered "+stack+"/"+svc, hamrmw.FlashSuccess)
	}
	return c.Redirect(http.StatusSeeOther, "/cron/executions?stack="+stack+"&service="+svc)
}

