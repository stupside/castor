// Package machine is a state machine over typed states and events, run by looplab/fsm and traced as it moves.
package machine

import (
	"context"
	"errors"
	"fmt"

	"github.com/looplab/fsm"

	"github.com/stupside/castor/internal/telemetry"
)

// Name is a state or an event, spelled by String wherever the machine shows it.
type Name interface {
	comparable
	String() string
}

// Edge is one move: the event, the states that take it, and the state it reaches.
type Edge[S, E Name] struct {
	On   E
	From []S
	To   S
}

// ErrRefused is an event the machine's state does not take.
var ErrRefused = errors.New("refused")

// Machine is a state machine whose every state and event is typed.
type Machine[S, E Name] struct {
	fsm    *fsm.FSM
	states map[string]S
	trace  *telemetry.MachineTrace
}

// New is the machine name, starting at initial and over at any of terminal, running after[e] once e is taken.
func New[S, E Name](name string, initial S, terminal []S, edges []Edge[S, E], after map[E]func(context.Context)) *Machine[S, E] {
	m := &Machine[S, E]{states: map[string]S{initial.String(): initial}}
	taken := map[E]bool{}
	events := make(fsm.Events, len(edges))
	for i, edge := range edges {
		from := make([]string, len(edge.From))
		for j, s := range edge.From {
			from[j] = m.spell(s)
		}
		events[i] = fsm.EventDesc{Name: edge.On.String(), Src: from, Dst: m.spell(edge.To)}
		taken[edge.On] = true
	}
	ends := make([]string, len(terminal))
	for i, s := range terminal {
		ends[i] = s.String()
	}
	m.trace = telemetry.Track(name, ends...)
	callbacks := fsm.Callbacks{"after_event": func(ctx context.Context, e *fsm.Event) { m.trace.Moved(ctx, e.Event, e.Src, e.Dst) }}
	for e, run := range after {
		if !taken[e] {
			// The engine would drop it without a word.
			panic(fmt.Sprintf("machine %s: no edge takes %s, so nothing would run after it", name, e))
		}
		callbacks["after_"+e.String()] = func(ctx context.Context, _ *fsm.Event) { run(ctx) }
	}
	m.fsm = fsm.NewFSM(initial.String(), events, callbacks)
	return m
}

func (m *Machine[S, E]) spell(s S) string {
	m.states[s.String()] = s
	return s.String()
}

// Fire takes e: staying put is no error, a state that does not take e is ErrRefused, and cancellation never aborts a move.
func (m *Machine[S, E]) Fire(ctx context.Context, e E) error {
	err := m.fsm.Event(context.WithoutCancel(ctx), e.String())
	if stayed, ok := errors.AsType[fsm.NoTransitionError](err); ok {
		return stayed.Err
	}
	if _, refused := errors.AsType[fsm.InvalidEventError](err); refused {
		return fmt.Errorf("%s in %s: %w", e, m.State(), ErrRefused)
	}
	return err
}

func (m *Machine[S, E]) Is(s S) bool  { return m.fsm.Is(s.String()) }
func (m *Machine[S, E]) Can(e E) bool { return m.fsm.Can(e.String()) }
func (m *Machine[S, E]) State() S     { return m.states[m.fsm.Current()] }

// End ends the trace of a machine its owner is done with before any terminal state.
func (m *Machine[S, E]) End(ctx context.Context) { m.trace.End(ctx) }
