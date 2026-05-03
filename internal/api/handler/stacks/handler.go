package stacks

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/stackcmd"

	"github.com/jamestiberiuskirk/stackr/internal/service"
)

// stackResponse is the JSON shape returned by /api/stacks endpoints.
type stackResponse struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	ComposePaths []string `json:"compose_paths"`
}

func toResponse(s stackcmd.StackInfo) stackResponse {
	return stackResponse{
		Name:         s.Name,
		Type:         string(s.Type),
		ComposePaths: s.ComposePaths,
	}
}

type handler struct {
	stackr *service.StackrService
}

// NewHandler creates a new stacks API handler.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// List handles GET /api/stacks — returns all discovered stacks.
func (h *handler) List(c echo.Context) error {
	stacks, err := h.stackr.ListStacks(c.Request().Context())
	if err != nil {
		return err
	}
	out := make([]stackResponse, 0, len(stacks))
	for _, s := range stacks {
		out = append(out, toResponse(s))
	}
	return c.JSON(http.StatusOK, map[string]any{"stacks": out})
}

// Get handles GET /api/stacks/:name — returns a single stack.
func (h *handler) Get(c echo.Context) error {
	name := c.Param("name")
	info, err := h.stackr.GetStack(c.Request().Context(), name)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}
	return c.JSON(http.StatusOK, toResponse(*info))
}
