package home

import (
	"net/http"

	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/service"
)

// Handler handles home page requests.
type handler struct {
	stackr *service.StackrService
}

// NewHandler creates a new home handler.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// GET /
func (h *handler) Index(c echo.Context) error {
	stacks, err := h.stackr.ListStacks(c.Request().Context())
	if err != nil {
		// Surface the error to the page rather than 500 — the daemon is still
		// operational; only the stacks readout is broken.
		return respond.HTML(c, http.StatusOK, homePage(c, nil, err))
	}
	return respond.HTML(c, http.StatusOK, homePage(c, stacks, nil))
}
