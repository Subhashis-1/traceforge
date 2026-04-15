// Package api provides HTTP handlers for the Traceforge API.
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

// sessionEventsResponse represents the response for SessionEventsHandler.
type sessionEventsResponse struct {
	SessionID  string          `json:"sessionId"`
	Events     []*models.Event `json:"events"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// RegisterSessionRoutes registers session-related routes under the given group.
func RegisterSessionRoutes(g *echo.Group, repo storage.Repository) {
	g.GET("/:session_id/events", SessionEventsHandler(repo))
}

// SessionEventsHandler handles GET /sessions/:session_id/events.
// Returns events for a specific session within an optional time range.
// Query parameters:
//   - from (optional): start time in RFC3339 format (default: 1 hour ago)
//   - to (optional): end time in RFC3339 format (default: now)
//   - limit (optional): max results (default: 20, max: 500)
func SessionEventsHandler(repo storage.Repository) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Extract and validate session_id from URL
		sessionIDStr := c.Param("session_id")
		sessionID, err := uuid.Parse(sessionIDStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "invalid session_id: must be a valid UUID",
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
			if parsed > 500 {
				parsed = 500
			}
			limit = parsed
		}

		// Query repository for events
		ctx := c.Request().Context()
		events, err := repo.ListEventsBySession(ctx, sessionID, from, to, limit)
		if err != nil {
			if err == storage.ErrNotFound {
				return c.JSON(http.StatusNotFound, map[string]string{
					"error": "session not found",
				})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to get session events: " + err.Error(),
			})
		}

		// Build response
		response := &sessionEventsResponse{
			SessionID:  sessionID.String(),
			Events:     events,
			NextCursor: "", // Pagination not implemented for MVP
		}

		return c.JSON(http.StatusOK, response)
	}
}

// GetSessionTraceIDHandler handles GET /sessions/:session_id/trace.
// Returns the trace ID associated with a session.
func GetSessionTraceIDHandler(repo storage.Repository) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Extract and validate session_id from URL
		sessionIDStr := c.Param("session_id")
		sessionID, err := uuid.Parse(sessionIDStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "invalid session_id: must be a valid UUID",
			})
		}

		// Get trace ID by session
		ctx := c.Request().Context()
		traceID, err := repo.GetTraceIDBySession(ctx, sessionID)
		if err != nil {
			if err == storage.ErrNotFound {
				return c.JSON(http.StatusNotFound, map[string]string{
					"error": "session not found",
				})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to get session trace: " + err.Error(),
			})
		}

		// Return trace ID
		response := map[string]string{
			"traceId": traceID.String(),
		}

		return c.JSON(http.StatusOK, response)
	}
}
