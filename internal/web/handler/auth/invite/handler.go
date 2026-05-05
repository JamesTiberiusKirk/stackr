package invite

import (
	"errors"
	"net/http"

	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/FyrmForge/hamr/pkg/respond"
	"github.com/FyrmForge/hamr/pkg/validate"
	"github.com/labstack/echo/v4"

	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/service"
	"github.com/jamestiberiuskirk/stackr/internal/web/components/form"
)

// AcceptForm holds the redemption form values. Token is delivered via the
// initial GET querystring then carried through the POST as a hidden field.
type AcceptForm struct {
	Token           string `form:"token" query:"token"`
	Password        string `form:"password"`
	ConfirmPassword string `form:"confirm_password"`
}

// handler owns the GET form + POST redeem for /accept-invite. Logged-in
// users are kept off these routes via auth.RequireNotAuth on the route
// group — redeeming as someone else's invite while authenticated would be
// a footgun.
type handler struct {
	store    repo.Store
	inviter  *service.InviteService

	FormRules validate.Form
}

// NewHandler wires the handler to the store + invite service.
func NewHandler(store repo.Store, inviter *service.InviteService) *handler {
	return &handler{
		store:   store,
		inviter: inviter,
		FormRules: validate.NewForm(
			validate.WithOOBRenderer(form.OOBValidator),
			validate.Field("password", validate.Required, validate.MinLen(8)),
			validate.Field("confirm_password", validate.Required),
		),
	}
}

// GET /accept-invite?token=<token>
//
// We validate the token before rendering the form so users can't waste
// effort entering a password against a dead invite. The user's email is
// shown read-only as confirmation that they're setting a password for the
// account they expect.
func (h *handler) Page(c echo.Context) error {
	token := c.QueryParam("token")
	if token == "" {
		return respond.HTML(c, http.StatusBadRequest, invalidPage("Missing invite token. Ask your admin for a fresh link."))
	}

	email, err := h.lookupInviteEmail(c, token)
	if err != nil {
		return respond.HTML(c, statusForInviteError(err), invalidPage(messageForInviteError(err)))
	}

	return respond.HTML(c, http.StatusOK, invitePage(c, AcceptForm{Token: token}, email, nil))
}

// POST /accept-invite
//
// Re-validates the token (the user could sit on the form past expiry).
// On success: flashes "password set" and redirects to /login. We do NOT
// auto-create a session — keeping login as a single, well-trafficked
// authentication path is simpler than a second one with subtly different
// guarantees.
func (h *handler) Submit(c echo.Context) error {
	var f AcceptForm
	if err := c.Bind(&f); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid form data")
	}
	if f.Token == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing token")
	}

	if _, err := h.lookupInviteEmail(c, f.Token); err != nil {
		return respond.HTML(c, statusForInviteError(err), invalidPage(messageForInviteError(err)))
	}

	errs := h.FormRules.Validate(c)
	if errs == nil {
		errs = make(map[string]string)
	}
	if f.Password != f.ConfirmPassword {
		errs["confirm_password"] = "Passwords do not match"
	}
	if len(errs) > 0 {
		return respond.HTML(c, http.StatusUnprocessableEntity, inviteForm(c, f, errs))
	}

	log := logging.FromContext(c.Request().Context())
	if _, err := h.inviter.RedeemInvite(c.Request().Context(), f.Token, f.Password); err != nil {
		log.Warn("invite redeem failed", "error", err)
		return respond.HTML(c, statusForInviteError(err), invalidPage(messageForInviteError(err)))
	}

	middleware.SetFlash(c, "Password set. Please log in.", middleware.FlashSuccess)
	return respond.Redirect(c, "/login")
}

// lookupInviteEmail validates the token (presence, not-used, not-expired)
// and returns the owning user's email. Used by both Page and Submit so the
// rejection logic stays identical between GET and POST.
func (h *handler) lookupInviteEmail(c echo.Context, token string) (string, error) {
	row, err := h.store.GetInviteTokenByToken(c.Request().Context(), token)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", service.ErrInviteNotFound
	}
	if row.Used {
		return "", service.ErrInviteUsed
	}
	user, err := h.store.GetUserByID(c.Request().Context(), row.UserID)
	if err != nil {
		return "", err
	}
	if user == nil {
		return "", service.ErrUserNotFound
	}
	return user.Email, nil
}

// statusForInviteError maps invite-flow sentinels to HTTP status codes.
// not-found / used / expired all surface as 410 Gone (the resource existed
// but no longer applies); user-not-found is 404. Anything else bubbles as
// 500 via Echo's default handler.
func statusForInviteError(err error) int {
	switch {
	case errors.Is(err, service.ErrInviteNotFound),
		errors.Is(err, service.ErrInviteUsed),
		errors.Is(err, service.ErrInviteExpired):
		return http.StatusGone
	case errors.Is(err, service.ErrUserNotFound):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func messageForInviteError(err error) string {
	switch {
	case errors.Is(err, service.ErrInviteNotFound):
		return "This invite link is invalid. Ask your admin for a fresh one."
	case errors.Is(err, service.ErrInviteUsed):
		return "This invite has already been used. If you've forgotten your password, ask your admin to reissue."
	case errors.Is(err, service.ErrInviteExpired):
		return "This invite has expired. Ask your admin to reissue."
	case errors.Is(err, service.ErrUserNotFound):
		return "The account this invite was issued for no longer exists."
	}
	return "Something went wrong. Try again, or ask your admin to reissue."
}

