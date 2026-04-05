package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v5"
)

// APIKeyAuth returns middleware that validates the X-API-Key header
// against the expected key. Used to protect internal API endpoints.
func APIKeyAuth(apiKey string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			key := c.Request().Header.Get("X-API-Key")
			if key == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing API key")
			}
			if subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) != 1 {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid API key")
			}
			return next(c)
		}
	}
}
