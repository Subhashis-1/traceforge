// Package main provides the Traceforge API server.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	api "github.com/Subhashis-1/traceforge/internal/api"
	"github.com/Subhashis-1/traceforge/internal/storage"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	host := getEnv("API_HOST", "0.0.0.0")
	port := getEnv("API_PORT", "8080")

	// Initialize valid API keys map
	validKeys := map[string]struct{}{
		"demo-key": {},
	}

	e := echo.New()
	e.Use(api.CORS)
	e.Use(api.APIKeyAuth(validKeys))
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod: true,
		LogURI:    true,
		LogStatus: true,
		LogError:  true,
		LogValuesFunc: func(_ echo.Context, v middleware.RequestLoggerValues) error {
			if v.Error != nil {
				log.Printf("method=%s uri=%s status=%d error=%v", v.Method, v.URI, v.Status, v.Error)
				return nil
			}
			log.Printf("method=%s uri=%s status=%d", v.Method, v.URI, v.Status)
			return nil
		},
	}))
	e.Use(middleware.Recover())

	e.GET("/healthz", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	v1 := e.Group("/api/v1")
	api.RegisterTraceRoutes(v1.Group("/traces"), storage.NewMockRepository())
	api.RegisterSessionRoutes(v1.Group("/sessions"), storage.NewMockRepository())
	api.RegisterSearchRoutes(v1, storage.NewMockRepository())
	api.RegisterLatencyRoutes(v1, storage.NewMockRepository())

	addr := fmt.Sprintf("%s:%s", host, port)
	if err := e.Start(addr); err != nil {
		log.Fatal(err)
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
