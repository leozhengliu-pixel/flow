package main

import (
	"context"
	"net/http"

	"flow/api/internal/config"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

func configureTelemetry(ctx context.Context, config config.TelemetryConfig) (func(context.Context) error, error) {
	if !config.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}
	resources, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(config.ServiceName), semconv.DeploymentEnvironmentName(config.Environment)))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(resources))
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)), sdkmetric.WithResource(resources))
	otel.SetTracerProvider(provider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return func(ctx context.Context) error {
		traceErr := provider.Shutdown(ctx)
		metricErr := meterProvider.Shutdown(ctx)
		if traceErr != nil {
			return traceErr
		}
		return metricErr
	}, nil
}

func telemetryHandler(handler http.Handler, enabled bool) http.Handler {
	if !enabled {
		return handler
	}
	return otelhttp.NewHandler(handler, "flow.http", otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents))
}
