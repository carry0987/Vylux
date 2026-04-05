package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	redis "github.com/redis/go-redis/v9"
)

// RedisRateLimit applies a small fixed-window rate limit keyed by the caller identity.
func RedisRateLimit(client *redis.Client, prefix string, limit int64, window time.Duration, keyFunc func(*echo.Context) string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if client == nil {
				return next(c)
			}

			key := keyFunc(c)
			if key == "" {
				return next(c)
			}

			windowSeconds := int64(window / time.Second)
			if windowSeconds <= 0 {
				windowSeconds = 60
			}

			bucket := time.Now().Unix() / windowSeconds
			redisKey := fmt.Sprintf("ratelimit:%s:%s:%d", prefix, key, bucket)

			pipe := client.TxPipeline()
			countCmd := pipe.Incr(c.Request().Context(), redisKey)
			pipe.Expire(c.Request().Context(), redisKey, window+5*time.Second)
			if _, err := pipe.Exec(c.Request().Context()); err != nil {
				return next(c)
			}

			if countCmd.Val() > limit {
				c.Response().Header().Set("Retry-After", fmt.Sprintf("%d", windowSeconds))
				return c.String(http.StatusTooManyRequests, "Too Many Requests")
			}

			return next(c)
		}
	}
}

// HashRateLimitKey avoids storing raw credentials or tokens in Redis keys.
func HashRateLimitKey(raw string) string {
	if raw == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:8])
}
