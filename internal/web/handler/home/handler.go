package home

import (
	"net/http"

	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/compose"
	"github.com/jamestiberiuskirk/stackr/internal/service"
	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"
)

// Handler handles home page requests.
type handler struct {
	stackr *service.StackrService
}

// stackRow is the per-row view-model for the index. Bundles the discovered
// stack with the routes extracted from its compose file plus the live
// status snapshot — the templ shouldn't have to reach back into the
// service to render the table.
type stackRow struct {
	Stack  stackcmd.StackInfo
	Routes []compose.Route
	Status service.StackStatus
}

// NewHandler creates a new home handler.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// GET /
//
// Builds one stackRow per discovered stack. Inspection failures degrade
// to "no routes" for that single row rather than failing the whole page —
// a malformed compose file in stack X shouldn't hide stacks Y and Z.
func (h *handler) Index(c echo.Context) error {
	ctx := c.Request().Context()
	repoRoot := h.stackr.Config().RepoRoot
	stacks, err := h.stackr.ListStacks(ctx)
	if err != nil {
		// Surface the error to the page rather than 500 — the daemon is still
		// operational; only the stacks readout is broken.
		return respond.HTML(c, http.StatusOK, homePage(c, repoRoot, nil, err))
	}

	rows := make([]stackRow, 0, len(stacks))
	for _, s := range stacks {
		row := stackRow{Stack: s}
		if insp, err := h.stackr.InspectStack(ctx, s.Name); err == nil {
			row.Routes = insp.AllRoutes()
		}
		if status, err := h.stackr.StackStatus(ctx, s.Name); err == nil {
			row.Status = status
		}
		rows = append(rows, row)
	}
	return respond.HTML(c, http.StatusOK, homePage(c, repoRoot, rows, nil))
}
