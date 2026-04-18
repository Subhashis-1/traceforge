// Package api provides HTTP middleware for the Traceforge API.
package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

// CORS returns a middleware that handles Cross-Origin Resource Sharing.
// It allows origins from the CORS_ORIGINS environment variable (comma-separated)
// or defaults to "*" for development.
// Usage: e.Use(api.CORS)
func CORS(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Get allowed origins from environment variable
		allowedOrigins := os.Getenv("CORS_ORIGINS")
		var origin string
		
		if allowedOrigins == "" {
			// Default to wildcard for development
			origin = "*"
		} else {
			// Check if request origin is in the allowed list
			requestOrigin := c.Request().Header.Get("Origin")
			origins := strings.Split(allowedOrigins, ",")
			
			for _, o := range origins {
				o = strings.TrimSpace(o)
				if o == "*" || o == requestOrigin {
					origin = o
					break
				}
			}
			
			// If no match found and request has Origin header, deny
			if origin == "" && requestOrigin != "" {
				origin = origins[0] // Use first origin as fallback
			}
			
			// If still empty and no request origin, use wildcard
			if origin == "" {
				origin = "*"
			}
		}

		// Set CORS headers
		c.Response().Header().Set("Access-Control-Allow-Origin", origin)
		c.Response().Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Response().Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,X-API-Key")

		// Handle preflight OPTIONS requests
		if c.Request().Method == http.MethodOptions {
			return c.NoContent(http.StatusNoContent)
		}

		return next(c)
	}
}

// APIKeyAuth returns a middleware that validates API key authentication.
// validKeys is a map of valid API keys (created at server start).
// If the X-API-Key header is missing or invalid, returns 401 Unauthorized.
// On success, stores the key in the Echo context with key "apiKey".
// Usage: e.Use(api.APIKeyAuth(validKeys))
//revive:disable-next-line:exported
func APIKeyAuth(validKeys map[string]struct{}) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Get API key from header
			apiKey := c.Request().Header.Get("X-API-Key")
			
			// Check if header is missing
			if apiKey == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "invalid API key",
				})
			}
			
			// Validate API key
			if _, valid := validKeys[apiKey]; !valid {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "invalid API key",
				})
			}
			
			// Store API key in context for downstream use
			c.Set("apiKey", apiKey)
			
			return next(c)
		}
	}
}
