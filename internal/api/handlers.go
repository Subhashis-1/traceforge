// Package api provides HTTP handlers for the Traceforge API.
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/dgraph-io/ristretto"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// apiHandler implements the generated ServerInterface.
type apiHandler struct {
	repo  storage.Repository
	cache *ristretto.Cache
}

// NewHandler creates a new API handler with the given repository.
func NewHandler(repo storage.Repository) ServerInterface {
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e7,
		MaxCost:     1 << 30, // 1GB
		BufferItems: 64,
	})
	if err != nil {
		log.Fatalf("Failed to initialize Ristretto cache: %v", err)
	}
	return &apiHandler{repo: repo, cache: cache}
}

// GetLive implements GET /live.
func (h *apiHandler) GetLive(ctx echo.Context) error {
	return ctx.String(http.StatusOK, "ok")
}

// GetReady implements GET /ready.
func (h *apiHandler) GetReady(ctx echo.Context) error {
	if err := h.repo.HealthCheck(ctx.Request().Context()); err != nil {
		return ctx.String(http.StatusServiceUnavailable, "not ready")
	}
	return ctx.String(http.StatusOK, "ready")
}

// ListTraces implements GET /traces.
func (h *apiHandler) ListTraces(ctx echo.Context, params ListTracesParams) error {
	// Apply default limit
	limit := 50
	if params.Limit != nil {
		limit = int(*params.Limit)
		if limit <= 0 {
			limit = 50
		} else if limit > 1000 {
			limit = 1000
		}
	}

	// Decode cursor (paging state) if provided
	var pagingState []byte
	if params.Cursor != nil && *params.Cursor != "" {
		var err error
		pagingState, err = decodeCursor(*params.Cursor)
		if err != nil {
			return ctx.JSON(http.StatusBadRequest, Error{
				Code:    http.StatusBadRequest,
				Message: "Invalid cursor: must be valid base64",
			})
		}
	}

	// Call repository with paging state
	traces, nextPagingState, err := h.repo.ListTraces(ctx.Request().Context(), params.Service, params.From, params.To, limit, pagingState)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, Error{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		})
	}

	// Convert to API response
	apiTraces := make([]Trace, len(traces))
	for i, t := range traces {
		apiTraces[i] = toAPITrace(t)
	}

	// Encode next paging state as base64 cursor
	var nextCursor *string
	if len(nextPagingState) > 0 {
		encoded := encodeCursor(nextPagingState)
		nextCursor = &encoded
	}

	response := TraceListResponse{
		Traces:     apiTraces,
		NextCursor: nextCursor,
	}

	return ctx.JSON(http.StatusOK, response)
}

// encodeCursor encodes a paging state []byte as a base64 string.
func encodeCursor(pagingState []byte) string {
	return base64.StdEncoding.EncodeToString(pagingState)
}

// decodeCursor decodes a base64 string to a paging state []byte.
func decodeCursor(cursor string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(cursor)
}

// GetTraceByID implements GET /traces/{trace_id}.
func (h *apiHandler) GetTraceByID(ctx echo.Context, traceID openapi_types.UUID) error {
	// Try fast lookup first
	trace, err := h.repo.GetTraceByID(ctx.Request().Context(), fromOpenAPIUUID(traceID))
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return ctx.JSON(http.StatusInternalServerError, Error{
			Code:    http.StatusInternalServerError,
			Message: "Failed to get trace",
		})
	}

	// If not found via fast lookup, try to compose from spans
	if errors.Is(err, storage.ErrNotFound) || trace == nil {
		trace, err = h.composeTraceFromSpans(ctx.Request().Context(), fromOpenAPIUUID(traceID))
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return ctx.JSON(http.StatusNotFound, Error{
					Code:    http.StatusNotFound,
					Message: "Trace not found",
				})
			}
			return ctx.JSON(http.StatusInternalServerError, Error{
				Code:    http.StatusInternalServerError,
				Message: "Failed to compose trace from spans",
			})
		}
	}

	// Get spans for the trace
	spans, err := h.repo.ListSpansByTrace(ctx.Request().Context(), fromOpenAPIUUID(traceID))
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return ctx.JSON(http.StatusInternalServerError, Error{
			Code:    http.StatusInternalServerError,
			Message: "Failed to get spans",
		})
	}

	// Build response
	apiSpans := make([]Span, len(spans))
	for i, s := range spans {
		apiSpans[i] = toAPISpan(s)
	}

	response := TraceDetail{
		Trace: toAPITrace(trace),
		Spans: apiSpans,
	}

	return ctx.JSON(http.StatusOK, response)
}

// ListSpansByTrace implements GET /traces/{trace_id}/spans.
func (h *apiHandler) ListSpansByTrace(ctx echo.Context, traceID openapi_types.UUID) error {
	spans, err := h.repo.ListSpansByTrace(ctx.Request().Context(), fromOpenAPIUUID(traceID))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ctx.JSON(http.StatusNotFound, Error{
				Code:    http.StatusNotFound,
				Message: "Trace not found",
			})
		}
		return ctx.JSON(http.StatusInternalServerError, Error{
			Code:    http.StatusInternalServerError,
			Message: "Failed to list spans",
		})
	}

	apiSpans := make([]Span, len(spans))
	for i, s := range spans {
		apiSpans[i] = toAPISpan(s)
	}

	return ctx.JSON(http.StatusOK, apiSpans)
}

// ListEventsBySession implements GET /sessions/{session_id}/events.
func (h *apiHandler) ListEventsBySession(ctx echo.Context, sessionID openapi_types.UUID, params ListEventsBySessionParams) error {
	// Apply default limit
	limit := 100
	if params.Limit != nil {
		limit = int(*params.Limit)
		if limit <= 0 {
			limit = 100
		} else if limit > 1000 {
			limit = 1000
		}
	}

	// Set default time range if not provided (last 24 hours)
	var start, end time.Time
	if params.From == nil {
		start = time.Now().Add(-24 * time.Hour)
	} else {
		start = *params.From
	}

	if params.To == nil {
		end = time.Now()
	} else {
		end = *params.To
	}

	events, err := h.repo.ListEventsBySession(ctx.Request().Context(), fromOpenAPIUUID(sessionID), start, end, limit)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ctx.JSON(http.StatusNotFound, Error{
				Code:    http.StatusNotFound,
				Message: "Session not found",
			})
		}
		return ctx.JSON(http.StatusInternalServerError, Error{
			Code:    http.StatusInternalServerError,
			Message: "Failed to list events",
		})
	}

	apiEvents := make([]Event, len(events))
	for i, e := range events {
		apiEvents[i] = toAPIEvent(e)
	}

	return ctx.JSON(http.StatusOK, apiEvents)
}

// SearchTraces implements GET /search.
func (h *apiHandler) SearchTraces(ctx echo.Context, _ SearchTracesParams) error {
	// TODO: Implement DSL search
	// For now, return a simple response
	return ctx.JSON(http.StatusOK, TraceListResponse{
		Traces:     []Trace{},
		NextCursor: nil,
	})
}

// composeTraceFromSpans builds a trace by aggregating spans.
func (h *apiHandler) composeTraceFromSpans(ctx context.Context, traceID uuid.UUID) (*models.Trace, error) {
	spans, err := h.repo.ListSpansByTrace(ctx, traceID)
	if err != nil {
		return nil, err
	}

	if len(spans) == 0 {
		return nil, storage.ErrNotFound
	}

	// Find the root span (span with no parent or parent == trace_id)
	var rootSpan *models.Span
	for _, s := range spans {
		if s.ParentID == uuid.Nil || s.ParentID == traceID {
			rootSpan = s
			break
		}
	}

	// If no root span found, use the earliest span
	if rootSpan == nil && len(spans) > 0 {
		rootSpan = spans[0]
		for _, s := range spans {
			if s.StartTime.Before(rootSpan.StartTime) {
				rootSpan = s
			}
		}
	}

	// Calculate trace duration from spans
	var maxEndTime time.Time
	for _, s := range spans {
		endTime := s.StartTime.Add(time.Duration(s.DurationMs) * time.Millisecond)
		if endTime.After(maxEndTime) {
			maxEndTime = endTime
		}
	}

	durationMs := int64(0)
	if rootSpan != nil {
		durationMs = int64(maxEndTime.Sub(rootSpan.StartTime) / time.Millisecond)
	}

	// Determine status from spans
	status := 0 // Default to 0 (ok)
	for _, s := range spans {
		if s.Tags != nil {
			if val, ok := s.Tags["error"]; ok && val != "" {
				status = 1 // Use 1 for error, or adjust as needed
				break
			}
		}
	}

	return &models.Trace{
		TraceID:     traceID,
		ServiceName: rootSpan.ServiceName,
		StartTime:   rootSpan.StartTime,
		DurationMs:  durationMs,
		Status:      status,
		RootSpanID:  rootSpan.SpanID,
	}, nil
}

// toAPITrace converts a models.Trace to api.Trace.
func toAPITrace(t *models.Trace) Trace {
	// Map status int to TraceStatus string
	var status TraceStatus
	switch t.Status {
	case 1:
		status = TraceStatusError
	case 0:
		status = TraceStatusOk
	default:
		status = TraceStatusUnknown
	}

	trace := Trace{
		TraceId:     toOpenAPIUUID(t.TraceID),
		ServiceName: t.ServiceName,
		StartTime:   t.StartTime,
		DurationMs:  t.DurationMs,
		Status:      status,
		Tags:        &t.Tags,
	}

	if t.RootSpanID != uuid.Nil {
		trace.RootSpanId = toOpenAPIUUIDPtr(t.RootSpanID)
	}

	return trace
}

// toAPISpan converts a models.Span to api.Span.
func toAPISpan(s *models.Span) Span {
	span := Span{
		SpanId:      toOpenAPIUUID(s.SpanID),
		TraceId:     toOpenAPIUUID(s.TraceID),
		ServiceName: s.ServiceName,
		StartTime:   s.StartTime,
		DurationMs:  s.DurationMs,
		Tags:        &s.Tags,
	}

	if s.ParentID != uuid.Nil {
		span.ParentId = toOpenAPIUUIDPtr(s.ParentID)
	}

	// models.Span does not have OperationName, so do not set it

	return span
}

// toAPIEvent converts a models.Event to api.Event.
func toAPIEvent(e *models.Event) Event {
	// Convert []byte payload to map[string]interface{} if possible, else nil
	var payload map[string]interface{}
	if len(e.Payload) > 0 {
		// Try to unmarshal as JSON
		_ = unmarshalPayload(e.Payload, &payload)
	}

	event := Event{
		EventId:   toOpenAPIUUID(e.EventID),
		SessionId: toOpenAPIUUID(e.SessionID),
		Timestamp: e.Timestamp,
		Payload:   payload,
		Tags:      &e.Tags,
	}

	// models.Event does not have EventType, so do not set it

	return event
}

// unmarshalPayload tries to unmarshal a []byte payload into a map[string]interface{}.
func unmarshalPayload(data []byte, out *map[string]interface{}) error {
	if len(data) == 0 {
		*out = nil
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		*out = nil
		return err
	}
	*out = m
	return nil
}

// toOpenAPIUUID converts uuid.UUID to openapi_types.UUID.
func toOpenAPIUUID(id uuid.UUID) openapi_types.UUID {
	return openapi_types.UUID(id)
}

// toOpenAPIUUIDPtr converts uuid.UUID to *openapi_types.UUID.
func toOpenAPIUUIDPtr(id uuid.UUID) *openapi_types.UUID {
	u := toOpenAPIUUID(id)
	return &u
}

// fromOpenAPIUUID converts openapi_types.UUID to uuid.UUID.
func fromOpenAPIUUID(id openapi_types.UUID) uuid.UUID {
	return uuid.UUID(id)
}
