// Package api provides HTTP middleware for the Traceforge API.
package api

import (
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/time/rate"
)

var (
	// rateLimiters stores per-key rate limiters
	rateLimiters = make(map[string]*rate.Limiter)
	// rateLimitersMu protects the map
	rateLimitersMu sync.RWMutex

	// rateLimitConfig holds the global rate limit configuration
	rateLimitRPS   int
	rateLimitBurst int
	rateLimitOnce  sync.Once
)

// initRateLimitConfig reads rate limit configuration from environment variables.
func initRateLimitConfig() {
	rps := getEnvInt("RATE_LIMIT_RPS", 10)
	burst := getEnvInt("RATE_LIMIT_BURST", 20)

	rateLimitRPS = rps
	rateLimitBurst = burst
}

// getOrCreateLimiter retrieves or creates a rate limiter for the given API key.
func getOrCreateLimiter(apiKey string) *rate.Limiter {
	// Try read lock first for performance
	rateLimitersMu.RLock()
	limiter, exists := rateLimiters[apiKey]
	rateLimitersMu.RUnlock()

	if exists {
		return limiter
	}

	// Need to create new limiter
	rateLimitersMu.Lock()
	defer rateLimitersMu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists = rateLimiters[apiKey]; exists {
		return limiter
	}

	// Create new limiter with configured RPS and burst
	limiter = rate.NewLimiter(rate.Limit(rateLimitRPS), rateLimitBurst)
	rateLimiters[apiKey] = limiter

	return limiter
}

// RateLimit returns a middleware that provides per-API-key rate limiting
// using a token bucket algorithm. The API key must be present in the Echo
// context (set by APIKeyAuth middleware).
// Configuration via environment variables:
//   - RATE_LIMIT_RPS: requests per second (default: 10)
//   - RATE_LIMIT_BURST: burst capacity (default: 20)
// Usage: e.Use(api.RateLimit)
func RateLimit(next echo.HandlerFunc) echo.HandlerFunc {
	// Initialize configuration once
	rateLimitOnce.Do(initRateLimitConfig)

	return func(c echo.Context) error {
		// Get API key from context (must be set by APIKeyAuth middleware)
		apiKeyValue := c.Get("apiKey")
		if apiKeyValue == nil {
			// No API key in context - skip rate limiting
			return next(c)
		}

		apiKey, ok := apiKeyValue.(string)
		if !ok {
			// Invalid API key type - skip rate limiting
			return next(c)
		}

		// Get or create limiter for this API key
		limiter := getOrCreateLimiter(apiKey)

		// Check if request is allowed
		if !limiter.Allow() {
			// Rate limit exceeded
			c.Response().Header().Set("Retry-After", "1")
			return c.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "rate limit exceeded",
			})
		}

		// Calculate rate limit headers
		// Limit: maximum requests per second
		c.Response().Header().Set("X-RateLimit-Limit", strconv.Itoa(rateLimitRPS))

		// Remaining: tokens currently available
		remaining := limiter.Tokens()
		c.Response().Header().Set("X-RateLimit-Remaining", strconv.FormatInt(int64(remaining), 10))

		// Reset: seconds until next token
		if remaining < float64(rateLimitBurst) {
			resetSeconds := limiter.Reserve().DelayFrom(time.Now()).Seconds()
			c.Response().Header().Set("X-RateLimit-Reset", strconv.FormatFloat(resetSeconds, 'f', 0, 64))
		} else {
			c.Response().Header().Set("X-RateLimit-Reset", "0")
		}

		return next(c)
	}
}

// getEnvInt reads an integer from environment variable with a default value.
func getEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}

	return intValue
}
