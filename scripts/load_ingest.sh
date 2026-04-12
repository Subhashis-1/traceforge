#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: bash scripts/load_ingest.sh [TARGET_URL]

Runs a reproducible k6 load test against the OTLP HTTP ingestion endpoint.

Environment variables:
  TARGET_URL        Target OTLP HTTP endpoint.
                    Default: http://localhost:4318/v1/traces
  RATE              Request rate per second. Default: 10000
  DURATION          Test duration. Default: 60s
  P95_MS            Maximum allowed p95 latency in milliseconds. Default: 150
  PREALLOCATED_VUS  k6 preallocated VUs. Default: 400
  MAX_VUS           k6 max VUs. Default: 2000
  SUMMARY_EXPORT    Summary output path. Default: ./k6-ingest-summary.json

The script exits non-zero if the k6 latency threshold fails.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

TARGET_URL="${1:-${TARGET_URL:-http://localhost:4318/v1/traces}}"
RATE="${RATE:-10000}"
DURATION="${DURATION:-60s}"
P95_MS="${P95_MS:-150}"
PREALLOCATED_VUS="${PREALLOCATED_VUS:-400}"
MAX_VUS="${MAX_VUS:-2000}"
SUMMARY_EXPORT="${SUMMARY_EXPORT:-./k6-ingest-summary.json}"

if ! command -v k6 >/dev/null 2>&1; then
  echo "k6 is required but was not found in PATH" >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required but was not found in PATH" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

PAYLOAD_GO="$TMP_DIR/payload.go"
PAYLOAD_BIN="$TMP_DIR/otlp_payload.bin"
K6_SCRIPT="$TMP_DIR/load_ingest.js"

cat >"$PAYLOAD_GO" <<'EOF'
package main

import (
  "os"

  collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
  commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
  resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
  tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
  "google.golang.org/protobuf/proto"
)

func main() {
  req := &collecttracev1.ExportTraceServiceRequest{
    ResourceSpans: []*tracev1.ResourceSpans{{
      Resource: &resourcev1.Resource{
        Attributes: []*commonv1.KeyValue{{
          Key: "service.name",
          Value: &commonv1.AnyValue{
            Value: &commonv1.AnyValue_StringValue{StringValue: "k6-load"},
          },
        }},
      },
      ScopeSpans: []*tracev1.ScopeSpans{{
        Spans: []*tracev1.Span{{
          TraceId:           []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 11},
          SpanId:            []byte{0, 0, 0, 0, 0, 0, 0, 12},
          Name:              "load-span",
          StartTimeUnixNano: 1710000000000000000,
          EndTimeUnixNano:   1710000000100000000,
          Attributes: []*commonv1.KeyValue{{
            Key: "load.test",
            Value: &commonv1.AnyValue{
              Value: &commonv1.AnyValue_StringValue{StringValue: "true"},
            },
          }},
        }},
      }},
    }},
  }

  data, err := proto.Marshal(req)
  if err != nil {
    panic(err)
  }

  if err := os.WriteFile(os.Args[1], data, 0o644); err != nil {
    panic(err)
  }
}
EOF

go run "$PAYLOAD_GO" "$PAYLOAD_BIN"

cat >"$K6_SCRIPT" <<'EOF'
import http from 'k6/http';
import { Rate } from 'k6/metrics';

const payload = open(__ENV.OTLP_PAYLOAD, 'b');
const ingestErrors = new Rate('ingest_errors');

export const options = {
  scenarios: {
    otlp_ingest: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE),
      timeUnit: '1s',
      duration: __ENV.DURATION,
      preAllocatedVUs: Number(__ENV.PREALLOCATED_VUS),
      maxVUs: Number(__ENV.MAX_VUS),
    },
  },
  thresholds: {
    'http_req_duration{endpoint:otlp_http}': [`p(95)<${__ENV.P95_MS}`],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export default function () {
  const response = http.post(__ENV.TARGET_URL, payload, {
    headers: {
      'Content-Type': 'application/x-protobuf',
    },
    tags: {
      endpoint: 'otlp_http',
    },
  });

  ingestErrors.add(response.status !== 200);
}
EOF

echo "Running k6 load test against $TARGET_URL"
echo "Rate: ${RATE} req/s for ${DURATION}"
echo "Threshold: p95 <= ${P95_MS}ms"

OTLP_PAYLOAD="$PAYLOAD_BIN" \
TARGET_URL="$TARGET_URL" \
RATE="$RATE" \
DURATION="$DURATION" \
P95_MS="$P95_MS" \
PREALLOCATED_VUS="$PREALLOCATED_VUS" \
MAX_VUS="$MAX_VUS" \
k6 run --summary-export "$SUMMARY_EXPORT" "$K6_SCRIPT"

echo "k6 summary exported to $SUMMARY_EXPORT"
