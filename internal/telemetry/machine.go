package telemetry

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/stupside/castor/internal/castid"
)

// MachineTrace records a state machine as one span per state visited, so a trace shows
// where a cast spent its time while it runs: a state's span is exported when the
// machine leaves it. Entering a terminal state records an instant span, since
// nothing leaves it. Each span parents under the span context of the event that
// entered it (a self-transition is an event on the state's span), and each visit's duration feeds castor.fsm.state.duration.
type MachineTrace struct {
	machine  string
	terminal []string

	mu    sync.Mutex
	span  trace.Span
	state string
	since time.Time
}

// Track traces the machine named machine, whose visits end at any of terminal.
func Track(machine string, terminal ...string) *MachineTrace {
	return &MachineTrace{machine: machine, terminal: terminal, since: time.Now()}
}

// Moved records the machine taking event from one state to another, or staying where it is.
func (m *MachineTrace) Moved(ctx context.Context, event, from, to string) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.span == nil {
		m.enter(ctx, from, m.since, "")
	}
	if from == to {
		// A self-transition is something that happened in the state, not a new visit.
		m.span.AddEvent(event)
		return
	}
	m.span.SetAttributes(attribute.String(keyExit, event))
	m.leave(ctx, now)
	m.enter(ctx, to, now, event)
	if slices.Contains(m.terminal, to) {
		m.span.End(trace.WithTimestamp(now))
		m.span = nil
	}
	slog.DebugContext(ctx, "state transition", "machine", m.machine, "event", event, "from", from, "to", to)
}

// End ends the visit the machine was left in, for an owner done with it before any terminal state.
func (m *MachineTrace) End(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.span != nil {
		m.leave(ctx, time.Now())
	}
}

func (m *MachineTrace) enter(ctx context.Context, state string, at time.Time, event string) {
	attrs := []attribute.KeyValue{attribute.String(keyMachine, m.machine), attribute.String(keyState, state)}
	if event != "" {
		attrs = append(attrs, attribute.String(keyEvent, event))
	}
	if id, ok := castid.From(ctx); ok {
		attrs = append(attrs, attribute.String(keyCastID, id))
	}
	_, m.span = otel.Tracer(scope).Start(ctx, m.machine+"."+state, trace.WithTimestamp(at), trace.WithAttributes(attrs...))
	m.state, m.since = state, at
}

func (m *MachineTrace) leave(ctx context.Context, at time.Time) {
	m.span.End(trace.WithTimestamp(at))
	m.span = nil
	hist, err := otel.Meter(scope).Float64Histogram(metricStateDuration, metric.WithUnit("s"), metric.WithDescription("Time a state machine spent in a state"), durationBuckets)
	if err != nil {
		otel.Handle(err)
		return
	}
	hist.Record(context.WithoutCancel(ctx), at.Sub(m.since).Seconds(),
		metric.WithAttributes(attribute.String(keyMachine, m.machine), attribute.String(keyState, m.state)))
}
