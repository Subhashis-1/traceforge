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

	handler := api.NewHandler(storage.NewMockRepository())

	v1 := e.Group("/api/v1")
	RegisterTraceRoutes(v1.Group("/traces"), handler)
	RegisterSessionRoutes(v1.Group("/sessions"), handler)
	RegisterSearchRoutes(v1.Group("/search"), handler)
	RegisterLatencyRoutes(v1.Group("/services"))

	addr := fmt.Sprintf("%s:%s", host, port)
	if err := e.Start(addr); err != nil {
		log.Fatal(err)
	}
}

func RegisterTraceRoutes(g *echo.Group, handler api.ServerInterface) {
	wrapper := api.ServerInterfaceWrapper{Handler: handler}
	g.GET("", wrapper.ListTraces)
	g.GET("/:trace_id", wrapper.GetTraceByID)
	g.GET("/:trace_id/spans", wrapper.ListSpansByTrace)
}

func RegisterSessionRoutes(g *echo.Group, handler api.ServerInterface) {
	wrapper := api.ServerInterfaceWrapper{Handler: handler}
	g.GET("/:session_id/events", wrapper.ListEventsBySession)
}

func RegisterSearchRoutes(g *echo.Group, handler api.ServerInterface) {
	wrapper := api.ServerInterfaceWrapper{Handler: handler}
	g.GET("", wrapper.SearchTraces)
}

func RegisterLatencyRoutes(g *echo.Group) {
	g.GET("", func(c echo.Context) error {
		return c.JSON(http.StatusNotImplemented, map[string]string{
			"message": "services latency routes not implemented yet",
		})
	})
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
