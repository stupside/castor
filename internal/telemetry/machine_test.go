package telemetry_test

import (
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stupside/castor/internal/telemetry"
)

func TestAMachineIsOneSpanPerStateExportedAsItIsLeft(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)))
	t.Cleanup(func() { otel.SetTracerProvider(old) })

	track := telemetry.Track("attempt", "finished")
	track.Moved(t.Context(), "inspect", "preparing", "inspecting")
	if got := len(exporter.GetSpans()); got != 1 {
		t.Fatalf("after leaving the first state spans = %d, want 1 (the open one must not export yet)", got)
	}
	track.Moved(t.Context(), "poll", "inspecting", "inspecting")
	track.Moved(t.Context(), "done", "inspecting", "finished")

	var names []string
	for _, s := range exporter.GetSpans() {
		names = append(names, s.Name)
	}
	want := []string{"attempt.preparing", "attempt.inspecting", "attempt.finished"}
	if len(names) != len(want) {
		t.Fatalf("spans = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("spans = %v, want %v", names, want)
		}
	}
}

func TestAMachineEndedBeforeAnyTerminalStateExportsTheStateItWasLeftIn(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)))
	t.Cleanup(func() { otel.SetTracerProvider(old) })

	track := telemetry.Track("rate")
	track.Moved(t.Context(), "lifted", "steady", "trial")
	track.End(t.Context())
	track.End(t.Context())

	spans := exporter.GetSpans()
	if len(spans) != 2 || spans[1].Name != "rate.trial" {
		t.Fatalf("spans = %v, want rate.steady then rate.trial, once", spans)
	}
}

func TestASelfTransitionStaysInItsStateAsAnEvent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)))
	t.Cleanup(func() { otel.SetTracerProvider(old) })

	track := telemetry.Track("cast", "ended")
	track.Moved(t.Context(), "rank", "measuring", "measuring")
	track.Moved(t.Context(), "end", "measuring", "ended")

	spans := exporter.GetSpans()
	if len(spans) != 2 || spans[0].Name != "cast.measuring" || len(spans[0].Events) != 1 {
		t.Fatalf("spans = %v, want one cast.measuring carrying the rank event, then cast.ended", spans)
	}
}
