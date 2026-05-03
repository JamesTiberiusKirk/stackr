package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// BearerAuth returns middleware that requires an Authorization: Bearer <token>
// header matching the configured token. The comparison is constant-time so
// timing attacks cannot leak the token byte-by-byte. A handler is rejected
// with 401 if the header is missing, malformed, or the token does not match.
func BearerAuth(token string) echo.MiddlewareFunc {
	expected := []byte(token)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if len(expected) == 0 {
				return echo.NewHTTPError(http.StatusInternalServerError, "auth not configured")
			}
			header := c.Request().Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing or malformed Authorization header")
			}
			provided := []byte(strings.TrimSpace(strings.TrimPrefix(header, prefix)))
			if subtle.ConstantTimeCompare(provided, expected) != 1 {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
			}
			return next(c)
		}
	}
}
