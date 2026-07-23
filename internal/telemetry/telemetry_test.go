package telemetry

import (
	"context"
	"testing"
)

func TestSetupNoneReturnsNoopProviders(t *testing.T) {
	p, err := Setup(context.Background(), Config{ServiceName: "test", Exporter: "none"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if p.TracerProvider == nil || p.MeterProvider == nil {
		t.Fatal("Setup returned nil providers")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestSetupStdout(t *testing.T) {
	p, err := Setup(context.Background(), Config{ServiceName: "test", Exporter: "stdout"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	tracer := p.TracerProvider.Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")
	span.End()

	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestSetupRejectsUnknownExporter(t *testing.T) {
	if _, err := Setup(context.Background(), Config{ServiceName: "test", Exporter: "bogus"}); err == nil {
		t.Fatal("Setup succeeded with an unknown exporter, want error")
	}
}
