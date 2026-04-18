package otel

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Subhashis-1/traceforge/internal/storage"
)

type otlpHTTPTraceRequest struct {
	ResourceSpans []otlpHTTPResourceSpans `json:"resourceSpans"`
}

type otlpHTTPResourceSpans struct {
	ScopeSpans                  []otlpHTTPScopeSpans `json:"scopeSpans"`
	InstrumentationLibrarySpans []otlpHTTPScopeSpans `json:"instrumentationLibrarySpans"`
}

type otlpHTTPScopeSpans struct {
	Spans []otlpHTTPSpan `json:"spans"`
}

type otlpHTTPSpan struct {
	TraceID string `json:"traceId"`
}

// CorrelationMiddleware correlates an incoming Session-Id header with the
// first trace ID found in an OTLP HTTP JSON payload. Failures are logged and
// never block the wrapped request.
func CorrelationMiddleware(repo storage.Repository) func(http.Handler) http.Handler {
	logger := zap.L()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			correlateSessionTrace(r.Context(), repo, logger, r)
			next.ServeHTTP(w, r)
		})
	}
}

func correlateSessionTrace(ctx context.Context, repo storage.Repository, logger *zap.Logger, r *http.Request) {
	sessionHeader := r.Header.Get("Session-Id")
	if sessionHeader == "" {
		return
	}

	sessionID, err := uuid.Parse(sessionHeader)
	if err != nil {
		logger.Warn("invalid Session-Id header",
			zap.String("session_id", sessionHeader),
			zap.Error(err))
		return
	}

	traceID, ok, err := extractTraceIDFromRequest(r)
	if err != nil {
		logger.Warn("failed to inspect OTLP payload for session correlation",
			zap.String("session_id", sessionID.String()),
			zap.Error(err))
		return
	}
	if !ok {
		return
	}

	if err := repo.CreateSessionTraceMap(ctx, sessionID, traceID); err != nil {
		logger.Warn("failed to persist session-trace mapping",
			zap.String("session_id", sessionID.String()),
			zap.String("trace_id", traceID.String()),
			zap.Error(err))
	}
}

func extractTraceIDFromRequest(r *http.Request) (uuid.UUID, bool, error) {
	if r.Body == nil {
		return uuid.Nil, false, nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return uuid.Nil, false, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	if len(body) == 0 {
		return uuid.Nil, false, nil
	}

	var payload otlpHTTPTraceRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		return uuid.Nil, false, nil
	}

	for _, rs := range payload.ResourceSpans {
		if traceID, ok, err := firstTraceID(rs.ScopeSpans); ok || err != nil {
			return traceID, ok, err
		}
		if traceID, ok, err := firstTraceID(rs.InstrumentationLibrarySpans); ok || err != nil {
			return traceID, ok, err
		}
	}

	return uuid.Nil, false, nil
}

func firstTraceID(groups []otlpHTTPScopeSpans) (uuid.UUID, bool, error) {
	for _, group := range groups {
		for _, span := range group.Spans {
			if span.TraceID == "" {
				continue
			}

			traceID, err := parseTraceID(span.TraceID)
			if err != nil {
				return uuid.Nil, false, err
			}

			return traceID, true, nil
		}
	}

	return uuid.Nil, false, nil
}

func parseTraceID(value string) (uuid.UUID, error) {
	if traceID, err := uuid.Parse(value); err == nil {
		return traceID, nil
	}

	raw, err := hex.DecodeString(value)
	if err != nil {
		return uuid.Nil, err
	}

	return uuid.FromBytes(raw)
}
