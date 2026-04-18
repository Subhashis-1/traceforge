package ingestion

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

// SessionBatchRequest represents the payload emitted by the browser SDK.
type SessionBatchRequest struct {
	SessionID string            `json:"sessionId"`
	Events    []json.RawMessage `json:"events"`
	TraceID   string            `json:"traceId,omitempty"`
}

type sessionBatchResponse struct {
	Status    string `json:"status"`
	Processed int    `json:"processed"`
}

type sessionHandler struct {
	repo   storage.Repository
	logger *zap.Logger
}

// NewSessionHandler returns an Echo handler for /v1/sessions ingestion.
func NewSessionHandler(repo storage.Repository) echo.HandlerFunc {
	h := &sessionHandler{
		repo:   repo,
		logger: zap.L(),
	}
	return h.handleSessionBatch
}

// RegisterSessionRoutes wires the session ingestion endpoint into an Echo router.
func RegisterSessionRoutes(e *echo.Echo, repo storage.Repository) {
	e.POST("/v1/sessions", NewSessionHandler(repo))
}

func (h *sessionHandler) handleSessionBatch(c echo.Context) error {
	var req SessionBatchRequest
	if err := c.Bind(&req); err != nil {
		h.logger.Warn("invalid session batch request body",
			zap.Error(err),
			zap.String("remote_addr", c.Request().RemoteAddr))
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid request body: " + err.Error(),
		})
	}

	sessionID, err := uuid.Parse(req.SessionID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid sessionId: " + err.Error(),
		})
	}

	traceID, err := parseOptionalUUID(req.TraceID)
	if err != nil {
		h.logger.Warn("invalid batch trace ID, ignoring",
			zap.String("session_id", sessionID.String()),
			zap.String("trace_id", req.TraceID),
			zap.Error(err))
		traceID = uuid.Nil
	}

	processed := 0
	ctx := c.Request().Context()
	for idx, rawEvent := range req.Events {
		ev, err := h.parseEvent(rawEvent, sessionID, traceID)
		if err != nil {
			h.logger.Warn("skipping invalid session event",
				zap.Int("index", idx),
				zap.String("session_id", sessionID.String()),
				zap.Error(err))
			continue
		}

		if err := h.repo.CreateSessionEvent(ctx, ev); err != nil {
			h.logger.Error("failed to store session event",
				zap.Int("index", idx),
				zap.String("session_id", ev.SessionID.String()),
				zap.String("event_id", ev.EventID.String()),
				zap.Error(err))
			continue
		}

		processed++
	}

	h.logger.Info("processed session batch",
		zap.String("session_id", sessionID.String()),
		zap.Int("processed", processed),
		zap.Int("received", len(req.Events)))

	return c.JSON(http.StatusOK, sessionBatchResponse{
		Status:    "ok",
		Processed: processed,
	})
}

func (h *sessionHandler) parseEvent(rawEvent json.RawMessage, sessionID, batchTraceID uuid.UUID) (*models.Event, error) {
	var ev models.Event
	if err := json.Unmarshal(rawEvent, &ev); err != nil {
		return nil, fmt.Errorf("unmarshal event: %w", err)
	}

	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(rawEvent, &rawFields); err != nil {
		return nil, fmt.Errorf("decode event fields: %w", err)
	}

	eventID, err := parseEventID(rawFields)
	if err != nil {
		return nil, err
	}

	timestamp, err := parseEventTimestamp(rawFields)
	if err != nil {
		return nil, err
	}

	traceID, err := parseEventTraceID(rawFields, batchTraceID)
	if err != nil {
		return nil, err
	}

	ev.EventID = eventID
	ev.Timestamp = timestamp
	ev.SessionID = sessionID
	ev.TraceID = traceID
	ev.Payload = append([]byte(nil), rawEvent...)
	ev.Tags = extractEventTags(rawFields)

	return &ev, nil
}

func parseEventID(rawFields map[string]json.RawMessage) (uuid.UUID, error) {
	if field, ok := rawFields["eventId"]; ok {
		var value string
		if err := json.Unmarshal(field, &value); err != nil {
			return uuid.Nil, fmt.Errorf("invalid eventId: %w", err)
		}
		if value != "" {
			parsed, err := uuid.Parse(value)
			if err != nil {
				return uuid.Nil, fmt.Errorf("invalid eventId: %w", err)
			}
			return parsed, nil
		}
	}

	return uuid.NewRandom()
}

func parseEventTimestamp(rawFields map[string]json.RawMessage) (time.Time, error) {
	field, ok := rawFields["ts"]
	if !ok {
		return time.Time{}, fmt.Errorf("missing ts")
	}

	var millis int64
	if err := json.Unmarshal(field, &millis); err != nil {
		return time.Time{}, fmt.Errorf("invalid ts: %w", err)
	}

	return time.UnixMilli(millis).UTC(), nil
}

func parseEventTraceID(rawFields map[string]json.RawMessage, batchTraceID uuid.UUID) (uuid.UUID, error) {
	if field, ok := rawFields["traceId"]; ok {
		var value string
		if err := json.Unmarshal(field, &value); err != nil {
			return uuid.Nil, fmt.Errorf("invalid traceId: %w", err)
		}
		if value == "" {
			return batchTraceID, nil
		}

		parsed, err := uuid.Parse(value)
		if err != nil {
			return uuid.Nil, fmt.Errorf("invalid traceId: %w", err)
		}
		return parsed, nil
	}

	return batchTraceID, nil
}

func parseOptionalUUID(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(value)
}

func extractEventTags(rawFields map[string]json.RawMessage) map[string]string {
	tags := make(map[string]string)

	for jsonKey, tagKey := range map[string]string{
		"type":   "type",
		"target": "target",
		"name":   "name",
		"url":    "url",
	} {
		var value string
		if field, ok := rawFields[jsonKey]; ok && json.Unmarshal(field, &value) == nil && value != "" {
			tags[tagKey] = value
		}
	}

	if len(tags) == 0 {
		return nil
	}

	return tags
}
