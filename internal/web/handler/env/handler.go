// Package env serves /env — the read/write surface for the repo's
// `.env` file plus a read-only readout of yaml-side env config
// (env.global, env.stacks.X) and the auto-provisioned pool vars.
//
// Edits go straight to the .env file via envfile.Update / Delete and
// emit one events row each for audit. Concurrency is naive — single
// operator assumption holds for now; multi-edit conflict handling
// belongs to a later phase.
package env

import (
	"errors"
	"net/http"

	hamrmw "github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/service"
)

// EnvForm captures the add/update form. Empty values are allowed —
// `KEY=` is a valid env line.
type EnvForm struct {
	Key   string `form:"key"`
	Value string `form:"value"`
}

type handler struct {
	stackr *service.StackrService
}

// NewHandler wires the env page to the stackr service.
func NewHandler(stackr *service.StackrService) *handler {
	return &handler{stackr: stackr}
}

// GET /env
func (h *handler) Page(c echo.Context) error {
	view, err := h.stackr.EnvOverview(c.Request().Context())
	if err != nil {
		return respond.HTML(c, http.StatusInternalServerError, envPage(c, view, err))
	}
	return respond.HTML(c, http.StatusOK, envPage(c, view, nil))
}

// POST /env
//
// Add or update a key/value pair. Empty key → 400; bad key shape → 400.
// On success we flash and redirect to GET /env so the page reflects the
// new state without a stale form re-render.
func (h *handler) Submit(c echo.Context) error {
	var f EnvForm
	if err := c.Bind(&f); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid form data")
	}
	if err := h.stackr.SetEnv(c.Request().Context(), f.Key, f.Value); err != nil {
		if errors.Is(err, service.ErrInvalidEnvKey) {
			hamrmw.SetFlash(c, "Invalid key — letters / digits / underscore only, must not start with a digit.", hamrmw.FlashError)
			return c.Redirect(http.StatusSeeOther, "/env")
		}
		hamrmw.SetFlash(c, "Failed to set "+f.Key+": "+err.Error(), hamrmw.FlashError)
		return c.Redirect(http.StatusSeeOther, "/env")
	}
	hamrmw.SetFlash(c, "Saved "+f.Key, hamrmw.FlashSuccess)
	return c.Redirect(http.StatusSeeOther, "/env")
}

// POST /env/:key/delete
func (h *handler) Delete(c echo.Context) error {
	key := c.Param("key")
	if key == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing key")
	}
	removed, err := h.stackr.DeleteEnv(c.Request().Context(), key)
	if err != nil {
		if errors.Is(err, service.ErrInvalidEnvKey) {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		hamrmw.SetFlash(c, "Failed to delete "+key+": "+err.Error(), hamrmw.FlashError)
		return c.Redirect(http.StatusSeeOther, "/env")
	}
	if !removed {
		hamrmw.SetFlash(c, "Key "+key+" was not present.", hamrmw.FlashInfo)
	} else {
		hamrmw.SetFlash(c, "Deleted "+key, hamrmw.FlashSuccess)
	}
	return c.Redirect(http.StatusSeeOther, "/env")
}
