package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

// TestBearerAuth exercises the auth gate from the outside: build an Echo
// context per case, invoke the middleware-wrapped handler, and assert the
// request reached the next handler iff the token matched. Uses a sentinel
// handler instead of inspecting middleware internals so behavior is verified
// at the actual chain edge.
func TestBearerAuth(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		header     string
		wantStatus int
		wantNext   bool
	}{
		{name: "correct token", configured: "correct-token", header: "Bearer correct-token", wantStatus: http.StatusOK, wantNext: true},
		{name: "wrong token", configured: "correct-token", header: "Bearer wrong-token", wantStatus: http.StatusUnauthorized, wantNext: false},
		{name: "empty header", configured: "correct-token", header: "", wantStatus: http.StatusUnauthorized, wantNext: false},
		{name: "no bearer prefix", configured: "correct-token", header: "correct-token", wantStatus: http.StatusUnauthorized, wantNext: false},
		{name: "basic auth prefix", configured: "correct-token", header: "Basic correct-token", wantStatus: http.StatusUnauthorized, wantNext: false},
		{name: "bearer with extra leading space", configured: "correct-token", header: "Bearer  correct-token", wantStatus: http.StatusOK, wantNext: true},
		{name: "empty token after bearer", configured: "correct-token", header: "Bearer ", wantStatus: http.StatusUnauthorized, wantNext: false},
		{name: "partial token", configured: "correct-token", header: "Bearer correct", wantStatus: http.StatusUnauthorized, wantNext: false},
		{name: "token with suffix", configured: "correct-token", header: "Bearer correct-token-extra", wantStatus: http.StatusUnauthorized, wantNext: false},
		// Defense-in-depth: an unconfigured token must reject everything (fail closed)
		// rather than letting the empty-string compare succeed.
		{name: "unconfigured token rejects empty bearer", configured: "", header: "Bearer ", wantStatus: http.StatusInternalServerError, wantNext: false},
		{name: "unconfigured token rejects valid-looking bearer", configured: "", header: "Bearer something", wantStatus: http.StatusInternalServerError, wantNext: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			reached := false
			handler := BearerAuth(tt.configured)(func(c echo.Context) error {
				reached = true
				return c.NoContent(http.StatusOK)
			})

			err := handler(c)
			if tt.wantStatus == http.StatusOK {
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, rec.Code)
			} else {
				var httpErr *echo.HTTPError
				require.ErrorAs(t, err, &httpErr)
				require.Equal(t, tt.wantStatus, httpErr.Code)
			}
			require.Equal(t, tt.wantNext, reached)
		})
	}
}
