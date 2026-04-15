// Package api provides HTTP handlers for the Traceforge API.
package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

// traceListResponse represents the response for ListTraces.
type traceListResponse struct {
	Traces     []*models.Trace `json:"traces"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// RegisterTraceRoutes registers trace-related routes under the given group.
func RegisterTraceRoutes(g *echo.Group, repo storage.Repository) {
	g.GET("", ListTracesHandler(repo))
	g.GET("/:trace_id", TraceDetailHandler(repo))
}

// ListTracesHandler handles GET /traces with query parameters:
//   - service (required): service name to filter by
//   - from (optional): start time in RFC3339 format (default: 1 hour ago)
//   - to (optional): end time in RFC3339 format (default: now)
//   - limit (optional): max results (default: 20, max: 1000)
//   - cursor (optional): base64-encoded paging state from previous response
func ListTracesHandler(repo storage.Repository) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Parse required service parameter
		service := c.QueryParam("service")
		if service == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "query parameter 'service' is required",
			})
		}

		// Parse time range parameters
		now := time.Now().UTC()
		from := now.Add(-time.Hour) // Default: 1 hour ago
		to := now                   // Default: now

		if fromStr := c.QueryParam("from"); fromStr != "" {
			parsed, err := time.Parse(time.RFC3339, fromStr)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": "invalid 'from' parameter: must be RFC3339 format",
				})
			}
			from = parsed
		}

		if toStr := c.QueryParam("to"); toStr != "" {
			parsed, err := time.Parse(time.RFC3339, toStr)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": "invalid 'to' parameter: must be RFC3339 format",
				})
			}
			to = parsed
		}

		// Parse limit parameter
		limit := 20 // Default
		if limitStr := c.QueryParam("limit"); limitStr != "" {
			parsed, err := strconv.Atoi(limitStr)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": "invalid 'limit' parameter: must be an integer",
				})
			}
			if parsed < 1 {
				parsed = 1
			}
			if parsed > 1000 {
				parsed = 1000
			}
			limit = parsed
		}

		// Parse cursor parameter (base64-encoded paging state)
		var pagingState []byte
		if cursorStr := c.QueryParam("cursor"); cursorStr != "" {
			decoded, err := base64.StdEncoding.DecodeString(cursorStr)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": "invalid 'cursor' parameter: must be base64-encoded",
				})
			}
			pagingState = decoded
		}

		// Query repository
		ctx := c.Request().Context()
		traces, nextCursor, err := repo.ListTraces(ctx, service, from, to, limit, pagingState)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to list traces: " + err.Error(),
			})
		}

		// Encode next cursor if present
		var nextCursorStr string
		if len(nextCursor) > 0 {
			nextCursorStr = base64.StdEncoding.EncodeToString(nextCursor)
		}

		// Return response
		response := &traceListResponse{
			Traces:     traces,
			NextCursor: nextCursorStr,
		}

		return c.JSON(http.StatusOK, response)
	}
}

// TraceDetailHandler handles GET /traces/:trace_id.
// Returns a trace by its UUID including all associated spans.
func TraceDetailHandler(repo storage.Repository) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Extract and validate trace_id from URL
		traceIDStr := c.Param("trace_id")
		traceID, err := uuid.Parse(traceIDStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "invalid trace_id: must be a valid UUID",
			})
		}

		// Get trace by ID
		ctx := c.Request().Context()
		trace, err := repo.GetTraceByID(ctx, traceID)
		if err != nil {
			if err == storage.ErrNotFound {
				return c.JSON(http.StatusNotFound, map[string]string{
					"error": "trace not found",
				})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to get trace: " + err.Error(),
			})
		}

		// Get spans for this trace
		spans, err := repo.ListSpansByTrace(ctx, traceID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to get spans: " + err.Error(),
			})
		}

		// Build response with trace and spans
		response := map[string]interface{}{
			"trace": trace,
			"spans": spans,
		}

		return c.JSON(http.StatusOK, response)
	}
}
