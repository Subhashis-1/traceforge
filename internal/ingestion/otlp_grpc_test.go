package ingestion

import (
	"context"
	"testing"

	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOTLPTraceGRPCServerExportSubmitsSpans(t *testing.T) {
	sink := &stubSink{}
	server := NewOTLPTraceGRPCServer(sink)

	traceID := uuid.New()
	_, err := server.Export(context.Background(), &collecttracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{{
			Resource:   &resourcev1.Resource{},
			ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{TraceId: traceID[:], SpanId: []byte{1, 2, 3, 4, 5, 6, 7, 8}, StartTimeUnixNano: 1, EndTimeUnixNano: 2}}}},
		}},
	})

	require.NoError(t, err)
	require.Len(t, sink.spans, 1)
}

func TestOTLPTraceGRPCServerExportReturnsResourceExhaustedWhenQueueIsFull(t *testing.T) {
	server := NewOTLPTraceGRPCServer(&stubSink{err: ErrQueueFull})

	_, err := server.Export(context.Background(), minimalTraceRequest())
	require.Error(t, err)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
}
