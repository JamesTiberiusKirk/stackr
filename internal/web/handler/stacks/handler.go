// Package stacks serves /stacks/{name} — a single-stack detail view that
// composes the discovered StackInfo with recent deployments and cron jobs
// scoped to that stack. The stacks list page is at /, owned by the home
// handler.
package stacks

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/compose"
	"github.com/jamestiberiuskirk/stackr/internal/cronjobs"
	"github.com/jamestiberiuskirk/stackr/internal/jobs"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/service"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

// detailDeployLimit caps the inline "Recent deployments" panel — full
// history is one click away on /deployments?stack=<name>.
const detailDeployLimit = 10

type handler struct {
	stackr *service.StackrService
	jobs   *jobs.Manager
}

// NewHandler creates a stacks handler. The jobs manager runs action
// operations (up/down/restart/update) asynchronously so HTTP requests
// don't block on docker compose calls; nil is acceptable in tests
// where the manager isn't needed.
func NewHandler(stackr *service.StackrService, jobMgr *jobs.Manager) *handler {
	return &handler{stackr: stackr, jobs: jobMgr}
}

// BulkForm captures the multi-stack action submission. Echo binds a
// repeated checkbox name (`stacks=A&stacks=B`) into the slice; action is
// the verb chosen by which submit button was clicked.
type BulkForm struct {
	Stacks []string `form:"stacks"`
	Action string   `form:"action"`
}

// Bulk runs the same action across multiple selected stacks. Sequential
// per-stack so audit rows land in stable order; partial failures don't
// roll back. Builds a one-line summary flash like "up: 2 ok, 1 failed
// (broken: missing var)".
//
// POST /stacks/bulk
func (h *handler) Bulk(c echo.Context) error {
	var f BulkForm
	if err := c.Bind(&f); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid form data")
	}
	if len(f.Stacks) == 0 {
		// Empty selection is a UI guard; with HTMX no nav we drop the
		// flash. Operators can see no rows were checked.
		return c.NoContent(http.StatusNoContent)
	}
	if f.Action == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing action")
	}

	if !knownAction(f.Action) {
		return echo.NewHTTPError(http.StatusBadRequest, "unknown action: "+f.Action)
	}
	for _, name := range f.Stacks {
		h.enqueueAction(name, f.Action)
	}
	// Per-stack lifecycle (pending → running → success/failed) lands on
	// the WS hub via jobs.Manager — that's the live feedback the operator
	// sees. No redirect, no flash.
	return c.NoContent(http.StatusNoContent)
}

// strings is still used elsewhere in this package (TrimSpace, etc); the
// bulk-summary helpers were removed when bulk became async — final
// per-stack outcomes now flow through the WS hub.
var _ = strings.TrimSpace

// Action queues a manual up/down/restart/update against a stack and
// returns immediately — the work runs in a background goroutine and
// publishes lifecycle events on `job:<id>` and `stack:<name>` topics
// for WS clients. The verb comes from the path parameter so one
// handler covers all four buttons.
//
// Synchronous validation (unknown action, invalid stack name) still
// surfaces via 400/404 directly. Anything that requires running
// docker compose is enqueued.
//
// POST /stacks/:name/:action
func (h *handler) Action(c echo.Context) error {
	name := c.Param("name")
	action := c.Param("action")
	if name == "" || action == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing stack name or action")
	}
	// Up-front validation that doesn't need to fork a goroutine: action
	// verb known, stack name well-formed and resolvable. This way an
	// invalid POST never queues an orphan job.
	if !knownAction(action) {
		return echo.NewHTTPError(http.StatusBadRequest, "unknown action: "+action)
	}
	if _, err := h.stackr.GetStack(c.Request().Context(), name); err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidStackName):
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		case errors.Is(err, service.ErrStackNotFound):
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	h.enqueueAction(name, action)
	// Lifecycle (pending → running → success/failed) flows over the WS
	// hub; the page stays put so banners survive. No redirect, no flash.
	return c.NoContent(http.StatusNoContent)
}

// enqueueAction is the shared "fork off the work" path used by both
// single-stack Action and the bulk handler. Captures the same trigger
// (manual) and propagates ctx so a goroutine cancellation could one
// day be plumbed through.
func (h *handler) enqueueAction(stack, action string) string {
	kind := "stack." + action
	return h.jobs.Enqueue(kind, stack, func(ctx context.Context) error {
		_, err := h.stackr.PerformAction(ctx, stack, action, repo.DeploymentTriggerManual)
		return err
	})
}

// knownAction is the verb whitelist Action enforces before enqueuing —
// keeps the audit table from filling with rows for typo'd verbs that
// would never run.
func knownAction(a string) bool {
	switch a {
	case service.ActionUp, service.ActionDown, service.ActionRestart, service.ActionUpdate:
		return true
	}
	return false
}

// GET /stacks/:name
//
// Loads the stack info plus three side panels: recent deployments, cron
// jobs whose Stack matches name, and recent cron executions for that stack.
// Side-panel failures are non-fatal — we degrade individual cards rather
// than 500 the whole page, so a broken compose file doesn't hide deploy
// history.
func (h *handler) Detail(c echo.Context) error {
	name := c.Param("name")
	if name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing stack name")
	}

	ctx := c.Request().Context()
	info, err := h.stackr.GetStack(ctx, name)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidStackName):
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		case errors.Is(err, service.ErrStackNotFound):
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Side-panel data — best-effort. Each error gets bubbled into the page.
	deployments, deployErr := h.stackr.ListDeployments(ctx, repo.DeploymentFilter{
		Stack: name,
		Limit: detailDeployLimit,
	})
	executions, execErr := h.stackr.ListCronExecutions(ctx, repo.CronExecutionFilter{
		Stack: name,
		Limit: detailDeployLimit,
	})

	jobs := []cronjobs.JobInfo{}
	allJobs, jobsErr := h.stackr.ListCronJobs(ctx)
	if jobsErr == nil {
		for _, j := range allJobs {
			if j.Stack == name {
				jobs = append(jobs, j)
			}
		}
	}

	inspection, inspectErr := h.stackr.InspectStack(ctx, name)
	status, statusErr := h.stackr.StackStatus(ctx, name)

	return respond.HTML(c, http.StatusOK, stackPage(c, stackPageData{
		Info:        info,
		Deployments: deployments,
		DeployErr:   deployErr,
		Jobs:        jobs,
		JobsErr:     jobsErr,
		Executions:  executions,
		ExecErr:     execErr,
		Inspection:  inspection,
		InspectErr:  inspectErr,
		Status:      status,
		StatusErr:   statusErr,
	}))
}

// stackPageData bundles everything the detail templ renders. Keeping it as
// a struct (rather than ten positional args) makes future panels cheap to
// add — no signature churn rippling through the templ.
type stackPageData struct {
	Info        *stackcmd.StackInfo
	Deployments []repo.Deployment
	DeployErr   error
	Jobs        []cronjobs.JobInfo
	JobsErr     error
	Executions  []repo.CronExecution
	ExecErr     error
	Inspection  compose.Inspection
	InspectErr  error
	Status      service.StackStatus
	StatusErr   error
}
