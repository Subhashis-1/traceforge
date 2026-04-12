// Package otel provides OpenTelemetry tracing utilities for Traceforge.
package otel

import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	traceapi "go.opentelemetry.io/otel/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

var tracer traceapi.Tracer

// InitTracer sets up OpenTelemetry tracing and returns a shutdown function.
func InitTracer(serviceName string, ingestEndpoint string) (func(context.Context) error, error) {
	ctx := context.Background()

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(ingestEndpoint), otlptracehttp.WithInsecure())
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	tracer = otel.Tracer(serviceName)

	return provider.Shutdown, nil
}

// Tracer returns the global tracer.
func Tracer() traceapi.Tracer {
	return tracer
}
