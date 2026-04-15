package ingestion

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/google/uuid"

	"github.com/Subhashis-1/traceforge/internal/models"
)

// SpanSink is the minimal submission contract the OTLP handlers need.
type SpanSink interface {
	Submit(span *models.Span) error
}

// NewOTLPHTTPHandler creates an HTTP handler for OTLP trace export requests.
func NewOTLPHTTPHandler(sink SpanSink) http.Handler {
	getMetrics()

	return instrumentHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		if len(body) == 0 {
			http.Error(w, "empty payload", http.StatusBadRequest)
			return
		}

		var unmarshaler ptrace.ProtoUnmarshaler
		traces, err := unmarshaler.UnmarshalTraces(body)
		if err != nil {
			http.Error(w, "invalid OTLP payload", http.StatusBadRequest)
			return
		}

		if err := submitTraces(context.Background(), sink, traces); err != nil {
			writeIngestError(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
}

func submitExportRequest(ctx context.Context, sink SpanSink, req *collecttracev1.ExportTraceServiceRequest) error {
	if req == nil {
		return fmt.Errorf("nil request")
	}

	payload, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal export request: %w", err)
	}

	var unmarshaler ptrace.ProtoUnmarshaler
	traces, err := unmarshaler.UnmarshalTraces(payload)
	if err != nil {
		return fmt.Errorf("decode export request: %w", err)
	}

	return submitTraces(ctx, sink, traces)
}

func submitTraces(_ context.Context, sink SpanSink, traces ptrace.Traces) error {
       spans, err := convertPDataTracesToSpans(traces)
       if err != nil {
	       return err
       }

       for _, span := range spans {
	       if err := sink.Submit(span); err != nil {
		       return err
	       }
       }

       return nil
}

func convertPDataTracesToSpans(traces ptrace.Traces) ([]*models.Span, error) {
	var spans []*models.Span

	resourceSpans := traces.ResourceSpans()
	for i := 0; i < resourceSpans.Len(); i++ {
		rs := resourceSpans.At(i)
		serviceName := serviceNameFromResource(rs.Resource())
		scopeSpans := rs.ScopeSpans()
		for j := 0; j < scopeSpans.Len(); j++ {
			ss := scopeSpans.At(j)
			otlpSpans := ss.Spans()
			for k := 0; k < otlpSpans.Len(); k++ {
				span, err := convertPDataSpan(serviceName, otlpSpans.At(k))
				if err != nil {
					return nil, err
				}
				spans = append(spans, span)
			}
		}
	}

	return spans, nil
}

func convertPDataSpan(serviceName string, otlpSpan ptrace.Span) (*models.Span, error) {
	traceID, err := traceIDToUUID(otlpSpan.TraceID())
	if err != nil {
		return nil, fmt.Errorf("invalid trace id: %w", err)
	}

	spanID, err := spanIDToUUIDFromPData(otlpSpan.SpanID())
	if err != nil {
		return nil, fmt.Errorf("invalid span id: %w", err)
	}

	parentID, err := spanIDToUUIDAllowEmptyFromPData(otlpSpan.ParentSpanID())
	if err != nil {
		return nil, fmt.Errorf("invalid parent span id: %w", err)
	}

	startTime := timestampToTime(otlpSpan.StartTimestamp())
	durationMs := int64(0)
	if endTime := timestampToTime(otlpSpan.EndTimestamp()); !endTime.Before(startTime) {
		durationMs = endTime.Sub(startTime).Milliseconds()
	}

	return &models.Span{
		TraceID:     traceID,
		SpanID:      spanID,
		ParentID:    parentID,
		ServiceName: serviceName,
		StartTime:   startTime,
		DurationMs:  durationMs,
		Tags:        attributesToTags(otlpSpan.Attributes()),
	}, nil
}

func serviceNameFromResource(resource pcommon.Resource) string {
	if value, ok := resource.Attributes().Get("service.name"); ok {
		serviceName := value.AsString()
		if serviceName != "" {
			return serviceName
		}
	}

	return "unknown-service"
}

func attributesToTags(attrs pcommon.Map) map[string]string {
	if attrs.Len() == 0 {
		return nil
	}

	tags := make(map[string]string, attrs.Len())
	attrs.Range(func(k string, v pcommon.Value) bool {
		tags[k] = v.AsString()
		return true
	})

	return tags
}

func timestampToTime(ts pcommon.Timestamp) time.Time {
	return time.Unix(0, int64(ts)).UTC()
}

func traceIDToUUID(traceID pcommon.TraceID) (uuid.UUID, error) {
	return uuid.FromBytes(traceID[:])
}

func spanIDToUUIDFromPData(spanID pcommon.SpanID) (uuid.UUID, error) {
	return spanIDToUUID(spanID[:])
}

func spanIDToUUIDAllowEmptyFromPData(spanID pcommon.SpanID) (uuid.UUID, error) {
	for _, b := range spanID {
		if b != 0 {
			return spanIDToUUIDFromPData(spanID)
		}
	}

	return uuid.Nil, nil
}

func spanIDToUUID(spanID []byte) (uuid.UUID, error) {
	if len(spanID) != 8 {
		return uuid.Nil, fmt.Errorf("expected 8 bytes, got %d", len(spanID))
	}

	var value uuid.UUID
	copy(value[8:], spanID)
	return value, nil
}

func writeIngestError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrQueueFull) {
		http.Error(w, "queue full", http.StatusTooManyRequests)
		return
	}

	http.Error(w, "failed to enqueue span", http.StatusInternalServerError)
}
