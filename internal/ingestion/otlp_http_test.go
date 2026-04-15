package ingestion

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Subhashis-1/traceforge/internal/models"
)

type stubSink struct {
	err   error
	spans []*models.Span
}

func (s *stubSink) Submit(span *models.Span) error {
	if s.err != nil {
		return s.err
	}
	s.spans = append(s.spans, span)
	return nil
}

func TestOTLPHTTPHandlerRejectsEmptyPayload(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", http.NoBody)
	rec := httptest.NewRecorder()

	NewOTLPHTTPHandler(&stubSink{}).ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOTLPHTTPHandlerSubmitsConvertedSpans(t *testing.T) {
	sink := &stubSink{}
	traceID := uuid.New()
	body := marshalTraceRequest(t, &collecttracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{
			{
				Resource: &resourcev1.Resource{
					Attributes: []*commonv1.KeyValue{
						{
							Key: "service.name",
							Value: &commonv1.AnyValue{
								Value: &commonv1.AnyValue_StringValue{StringValue: "checkout"},
							},
						},
					},
				},
				ScopeSpans: []*tracev1.ScopeSpans{
					{
						Spans: []*tracev1.Span{
							{
								TraceId:           traceID[:],
								SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
								ParentSpanId:      []byte{8, 7, 6, 5, 4, 3, 2, 1},
								StartTimeUnixNano: 1_000_000,
								EndTimeUnixNano:   4_000_000,
								Attributes: []*commonv1.KeyValue{
									{
										Key: "http.method",
										Value: &commonv1.AnyValue{
											Value: &commonv1.AnyValue_StringValue{StringValue: "GET"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	NewOTLPHTTPHandler(sink).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, sink.spans, 1)
	require.Equal(t, "checkout", sink.spans[0].ServiceName)
	require.EqualValues(t, 3, sink.spans[0].DurationMs)
	require.Equal(t, "GET", sink.spans[0].Tags["http.method"])
}

func TestOTLPHTTPHandlerReturns429WhenQueueIsFull(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(marshalTraceRequest(t, minimalTraceRequest())))
	rec := httptest.NewRecorder()

	NewOTLPHTTPHandler(&stubSink{err: ErrQueueFull}).ServeHTTP(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func minimalTraceRequest() *collecttracev1.ExportTraceServiceRequest {
	traceID := uuid.New()
	return &collecttracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{
			{
				Resource: &resourcev1.Resource{},
				ScopeSpans: []*tracev1.ScopeSpans{
					{
						Spans: []*tracev1.Span{
							{
								TraceId:           traceID[:],
								SpanId:            []byte{1, 1, 1, 1, 1, 1, 1, 1},
								StartTimeUnixNano: 1,
								EndTimeUnixNano:   2,
							},
						},
					},
				},
			},
		},
	}
}

func marshalTraceRequest(t *testing.T, req *collecttracev1.ExportTraceServiceRequest) []byte {
	t.Helper()

	body, err := proto.Marshal(req)
	require.NoError(t, err)
	return body
}
