package middleware

import (
	"net/http"
	"strings"

	"Vylux/internal/signature"

	"github.com/labstack/echo/v5"
)

// HMACSignature returns middleware that verifies the HMAC-SHA256 signature
// embedded in the image URL path.
//
// URL format: /img/{signature}/{options}/{encoded_source}.{format}
func HMACSignature(hmacSecret string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			sig := c.Param("sig")
			opts := c.Param("opts")
			source := c.Param("*")
			source = strings.TrimPrefix(source, "/")

			if sig == "" || source == "" {
				return echo.NewHTTPError(http.StatusBadRequest, "missing signature or source")
			}
			ok, err := signature.VerifyImage(hmacSecret, sig, opts, source)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
			if !ok {
				return echo.NewHTTPError(http.StatusForbidden, "invalid signature")
			}
			return next(c)
		}
	}
}
