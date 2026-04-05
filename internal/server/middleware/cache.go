package middleware

import (
	"github.com/labstack/echo/v5"
)

// CacheHeaders returns middleware that sets CDN-friendly cache headers.
//
// If immutable is true: "public, max-age=31536000, immutable" (1 year).
// Otherwise: "public, max-age=86400" (1 day).
//
// Cache headers are only applied to successful responses. When a handler
// returns an error (e.g. 404), the headers are removed before Echo's error
// handler writes the response, preventing CDNs from caching negative results.
func CacheHeaders(immutable bool) echo.MiddlewareFunc {
	cc := "public, max-age=86400"
	if immutable {
		cc = "public, max-age=31536000, immutable"
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			// Set headers optimistically — they will be flushed when the
			// handler calls WriteHeader(200) on the success path.
			c.Response().Header().Set("Cache-Control", cc)
			c.Response().Header().Set("Vary", "Accept")

			err := next(c)

			// On error the handler has NOT called WriteHeader yet, so we
			// can still remove the cache directives before Echo's error
			// handler writes the final response.
			if err != nil {
				c.Response().Header().Del("Cache-Control")
				c.Response().Header().Del("Vary")
			}
			return err
		}
	}
}
