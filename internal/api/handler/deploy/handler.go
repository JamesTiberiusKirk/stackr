package deploy

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/runner"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/service"
)

// deployRequest is the JSON payload for POST /api/deploy. ImageTag is accepted
// as an alias for Tag so existing CLI clients targeting the legacy daemon
// continue to work unchanged.
type deployRequest struct {
	Stack    string `json:"stack"`
	Tag      string `json:"tag"`
	ImageTag string `json:"image_tag"`
}

type deployResponse struct {
	ID         string `json:"id"`
	Stack      string `json:"stack"`
	Tag        string `json:"tag"`
	Status     string `json:"status"`
	Trigger    string `json:"trigger"`
	Stdout     string `json:"stdout,omitempty"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func toResponse(d *repo.Deployment) deployResponse {
	resp := deployResponse{
		ID:        d.ID,
		Stack:     d.Stack,
		Tag:       d.Tag,
		Status:    d.Status,
		Trigger:   d.Trigger,
		Stdout:    d.Stdout,
		Error:     d.Error,
		StartedAt: d.StartedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if d.FinishedAt != nil {
		resp.FinishedAt = d.FinishedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return resp
}

type handler struct {
	stackr *service.StackrService
}

// NewHandler creates a new deploy API handler.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// Submit handles POST /api/deploy. Body: {"stack":"…","tag":"…"}.
//
// Validation errors (unknown stack, invalid tag, auto-deploy disabled) become
// structured 4xx responses. Runner-level failures (the deploy ran but the
// underlying compose / git command returned non-zero) still return 200 with
// status=failed so the client always gets the deployment row id and can poll
// /api/deployments/:id without parsing error envelopes.
func (h *handler) Submit(c echo.Context) error {
	var req deployRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid JSON body")
	}

	if req.Stack == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "stack is required")
	}

	tag := req.Tag
	if tag == "" {
		tag = req.ImageTag
	}

	d, err := h.stackr.Deploy(c.Request().Context(), req.Stack, tag, repo.DeploymentTriggerAPI)

	switch {
	case errors.Is(err, service.ErrAutoDeployDisabled):
		return echo.NewHTTPError(http.StatusForbidden, err.Error())
	case errors.Is(err, service.ErrInvalidTag):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrStackNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	// Runner-level failure with a persisted deployment row → return the row.
	if err != nil && d != nil {
		var cmdErr *runner.CommandError
		if errors.As(err, &cmdErr) {
			return c.JSON(http.StatusOK, toResponse(d))
		}
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toResponse(d))
}
