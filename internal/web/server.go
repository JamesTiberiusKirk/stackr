package web

import (
	"context"

	hamrmw "github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/FyrmForge/hamr/pkg/server"
	"github.com/FyrmForge/hamr/pkg/auth"
	"github.com/FyrmForge/hamr/pkg/storage"

	"github.com/jamestiberiuskirk/stackr/internal/middleware"
	"github.com/jamestiberiuskirk/stackr/internal/repo"
	"github.com/jamestiberiuskirk/stackr/internal/service"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/auth/login"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/auth/register"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/about"
	"github.com/jamestiberiuskirk/stackr/internal/web/handler/home"
	"github.com/jamestiberiuskirk/stackr/internal/web/components"
)

// Deps holds the dependencies for route registration.
type Deps struct {
	Store          repo.Store
	BaseURL        string
	StaticBaseURL  string
	DevMode        bool
	SessionManager *auth.SessionManager
	AuthService    *service.AuthService
	FileStorage    storage.FileStorage
	Stackr         *service.StackrService
}

// RegisterRoutes registers all web route handlers on the server.
func RegisterRoutes(srv *server.Server, deps *Deps) {
	e := srv.Echo()

	// Content Security Policy.
	csp := "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'"

	// Site routes.
	site := e.Group("")
	site.Use(middleware.Logging())
	site.Use(hamrmw.ErrorPages(components.ErrorPage))
	site.Use(hamrmw.SecureWithConfig(hamrmw.SecureConfig{
		ContentSecurityPolicy: csp,
	}))
	site.Use(hamrmw.FlashWithConfig(hamrmw.FlashConfig{Secure: !deps.DevMode}))
	site.Use(hamrmw.CSRFWithConfig(hamrmw.CSRFConfig{Secure: !deps.DevMode}))

	auth := hamrmw.NewBrowserAuth(deps.SessionManager,
		hamrmw.WithSubjectLoader(func(reqCtx context.Context, id string) (any, error) {
			return deps.Store.GetUserByID(reqCtx, id)
		}),
		hamrmw.WithLoginRedirect("/login"),
		hamrmw.WithHomeRedirect("/"),
	)
	site.Use(auth.Load())

	homeHandler := home.NewHandler(deps.Stackr)
	site.GET("/", homeHandler.Index)

	// Auth routes — one page-package per page (login owns logout as its inverse action).
	loginHandler := login.NewHandler(deps.AuthService, deps.SessionManager)
	site.GET("/login", loginHandler.Page, auth.RequireNotAuth())
	site.POST("/login", loginHandler.Submit, auth.RequireNotAuth())
	site.POST("/login/validate/:field", loginHandler.FormRules.ValidationHandler("field"), auth.RequireNotAuth())
	site.POST("/logout", loginHandler.Logout, auth.RequireAuth())

	registerHandler := register.NewHandler(deps.AuthService, deps.SessionManager)
	site.GET("/register", registerHandler.Page, auth.RequireNotAuth())
	site.POST("/register", registerHandler.Submit, auth.RequireNotAuth())
	site.POST("/register/validate/:field", registerHandler.FormRules.ValidationHandler("field"), auth.RequireNotAuth())
}

// RegisterStaticPages registers handlers for static generation and runtime
// serving. Each call to StaticPage registers both a generation entry and a
// GET route. These handlers must not depend on database or session state.
func RegisterStaticPages(srv *server.Server) {
	h := about.NewHandler()
	srv.StaticPage("/about", h.About)
}
