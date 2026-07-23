package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type Config struct {
	ServiceName  string
	Exporter     string
	OTLPEndpoint string
}

type Provider struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	shutdown       func(context.Context) error
}

func Setup(ctx context.Context, cfg Config) (*Provider, error) {
	switch cfg.Exporter {
	case "", "none":
		return &Provider{
			TracerProvider: tracenoop.NewTracerProvider(),
			MeterProvider:  metricnoop.NewMeterProvider(),
			shutdown:       func(context.Context) error { return nil },
		}, nil
	case "stdout":
		return setupStdout(cfg)
	case "otlp":
		return setupOTLP(ctx, cfg)
	default:
		return nil, fmt.Errorf("telemetry: unknown exporter %q (want none, stdout, or otlp)", cfg.Exporter)
	}
}

func (p *Provider) Shutdown(ctx context.Context) error {
	return p.shutdown(ctx)
}

func newResource(serviceName string) *resource.Resource {
	return resource.NewSchemaless(semconv.ServiceName(serviceName))
}

func setupStdout(cfg Config) (*Provider, error) {
	traceExporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, fmt.Errorf("telemetry: stdout trace exporter: %w", err)
	}
	metricExporter, err := stdoutmetric.New()
	if err != nil {
		return nil, fmt.Errorf("telemetry: stdout metric exporter: %w", err)
	}

	res := newResource(cfg.ServiceName)
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExporter), sdktrace.WithResource(res))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)), sdkmetric.WithResource(res))

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	return &Provider{
		TracerProvider: tp,
		MeterProvider:  mp,
		shutdown: func(ctx context.Context) error {
			if err := tp.Shutdown(ctx); err != nil {
				return err
			}
			return mp.Shutdown(ctx)
		},
	}, nil
}

func setupOTLP(ctx context.Context, cfg Config) (*Provider, error) {
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlp trace exporter: %w", err)
	}
	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlp metric exporter: %w", err)
	}

	res := newResource(cfg.ServiceName)
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExporter), sdktrace.WithResource(res))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)), sdkmetric.WithResource(res))

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	return &Provider{
		TracerProvider: tp,
		MeterProvider:  mp,
		shutdown: func(ctx context.Context) error {
			if err := tp.Shutdown(ctx); err != nil {
				return err
			}
			return mp.Shutdown(ctx)
		},
	}, nil
}
