package ingestion

import (
	"context"
	"errors"

	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OTLPTraceGRPCServer implements the OTLP TraceService gRPC server.
type OTLPTraceGRPCServer struct {
	collecttracev1.UnimplementedTraceServiceServer
	sink SpanSink
}

// NewOTLPTraceGRPCServer creates a new OTLP TraceService gRPC server.
func NewOTLPTraceGRPCServer(sink SpanSink) *OTLPTraceGRPCServer {
	getMetrics()
	return &OTLPTraceGRPCServer{sink: sink}
}

// Export receives OTLP trace exports over gRPC and submits spans to the pipeline.
func (s *OTLPTraceGRPCServer) Export(ctx context.Context, req *collecttracev1.ExportTraceServiceRequest) (*collecttracev1.ExportTraceServiceResponse, error) {
	if req == nil {
		getMetrics().ingestFailedTotal.Inc()
		return nil, status.Error(codes.InvalidArgument, "nil request")
	}

	if err := submitExportRequest(ctx, s.sink, req); err != nil {
		if errors.Is(err, ErrQueueFull) {
			getMetrics().ingestFailedTotal.Inc()
			return nil, status.Error(codes.ResourceExhausted, "queue full")
		}

		getMetrics().ingestFailedTotal.Inc()
		return nil, status.Error(codes.Internal, "failed to enqueue span")
	}

	getMetrics().ingestRequestsTotal.Inc()
	return &collecttracev1.ExportTraceServiceResponse{}, nil
}
