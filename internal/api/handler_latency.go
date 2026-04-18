package api

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

type latencyMetricsResponse struct {
	Service string                  `json:"service"`
	Date    string                  `json:"date"`
	Metrics []*models.LatencyMetric `json:"metrics"`
}

// RegisterLatencyRoutes registers the latency aggregation endpoint.
func RegisterLatencyRoutes(g *echo.Group, repo storage.Repository) {
	g.GET("/services/latency", LatencyMetricsHandler(repo))
}

// LatencyMetricsHandler handles GET /services/latency.
func LatencyMetricsHandler(repo storage.Repository) echo.HandlerFunc {
	return func(c echo.Context) error {
		service := c.QueryParam("service")
		if service == "" {
			return c.JSON(http.StatusBadRequest, Error{
				Code:    http.StatusBadRequest,
				Message: "query parameter 'service' is required",
			})
		}

		bucket := c.QueryParam("bucket")
		if bucket == "" {
			bucket = "1m"
		}
		if bucket != "1m" && bucket != "5m" {
			return c.JSON(http.StatusBadRequest, Error{
				Code:    http.StatusBadRequest,
				Message: "query parameter 'bucket' must be one of: 1m, 5m",
			})
		}

		date := time.Now().UTC()
		metrics, err := repo.GetLatencyMetrics(c.Request().Context(), service, date)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, Error{
				Code:    http.StatusInternalServerError,
				Message: "failed to get latency metrics: " + err.Error(),
			})
		}

		if bucket == "5m" {
			metrics = aggregateLatencyMetrics(metrics, 5)
		}

		return c.JSON(http.StatusOK, latencyMetricsResponse{
			Service: service,
			Date:    date.Format("2006-01-02"),
			Metrics: metrics,
		})
	}
}

func aggregateLatencyMetrics(metrics []*models.LatencyMetric, size int) []*models.LatencyMetric {
	if size <= 1 || len(metrics) == 0 {
		return metrics
	}

	grouped := make(map[int]*models.LatencyMetric, len(metrics))
	for _, metric := range metrics {
		if metric == nil {
			continue
		}

		minute := (metric.Minute / size) * size
		current, ok := grouped[minute]
		if !ok {
			grouped[minute] = &models.LatencyMetric{
				Minute: minute,
				P95:    metric.P95,
				P99:    metric.P99,
			}
			continue
		}

		if metric.P95 > current.P95 {
			current.P95 = metric.P95
		}
		if metric.P99 > current.P99 {
			current.P99 = metric.P99
		}
	}

	out := make([]*models.LatencyMetric, 0, len(grouped))
	for minute := 0; minute < 24*60; minute += size {
		if metric, ok := grouped[minute]; ok {
			out = append(out, metric)
		}
	}

	return out
}
