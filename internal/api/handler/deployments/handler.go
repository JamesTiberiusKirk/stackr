package deployments

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
)

// Maximum results returned by List in a single response.
const maxListLimit = 200

type deploymentResponse struct {
	ID         string     `json:"id"`
	Stack      string     `json:"stack"`
	Tag        string     `json:"tag,omitempty"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger"`
	Error      string     `json:"error,omitempty"`
	Stdout     string     `json:"stdout,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func toResponse(d repo.Deployment) deploymentResponse {
	return deploymentResponse(d)
}

type handler struct {
	store repo.Store
}

// NewHandler creates a new deployments API handler.
func NewHandler(store repo.Store) *handler {
	return &handler{store: store}
}

// List handles GET /api/deployments — returns recent deployments, newest first.
// Query params: stack, status, limit, offset.
func (h *handler) List(c echo.Context) error {
	filter := repo.DeploymentFilter{
		Stack:  c.QueryParam("stack"),
		Status: c.QueryParam("status"),
		Limit:  parseIntDefault(c.QueryParam("limit"), 50),
		Offset: parseIntDefault(c.QueryParam("offset"), 0),
	}
	if filter.Limit > maxListLimit {
		filter.Limit = maxListLimit
	}

	rows, err := h.store.ListDeployments(c.Request().Context(), filter)
	if err != nil {
		return err
	}
	out := make([]deploymentResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, toResponse(r))
	}
	return c.JSON(http.StatusOK, map[string]any{"deployments": out})
}

// Get handles GET /api/deployments/:id.
func (h *handler) Get(c echo.Context) error {
	id := c.Param("id")
	d, err := h.store.GetDeploymentByID(c.Request().Context(), id)
	if err != nil {
		return err
	}
	if d == nil {
		return echo.NewHTTPError(http.StatusNotFound, "deployment not found")
	}
	return c.JSON(http.StatusOK, toResponse(*d))
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	return n
}
