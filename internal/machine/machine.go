// Package machine is a state machine over typed states and events, run by looplab/fsm and traced as it moves.
package machine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

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

// New is the machine name, starting at initial and over at any of terminal, running after[e] once e is taken; it panics on a table that cannot mean what its author wrote.
func New[S, E Name](name string, initial S, terminal []S, edges []Edge[S, E], after map[E]func(context.Context)) *Machine[S, E] {
	m := &Machine[S, E]{states: map[string]S{}}
	events := map[string]E{}
	spellEvent := func(e E) string {
		if known, ok := events[e.String()]; ok && known != e {
			panic(fmt.Sprintf("machine %s: two events are spelled %q", name, e))
		}
		events[e.String()] = e
		return e.String()
	}
	m.spell(name, initial)
	ends := make([]string, len(terminal))
	for i, s := range terminal {
		ends[i] = m.spell(name, s)
	}
	taken := map[string]bool{}
	moves := make(fsm.Events, len(edges))
	for i, edge := range edges {
		on := spellEvent(edge.On)
		from := make([]string, len(edge.From))
		for j, s := range edge.From {
			from[j] = m.spell(name, s)
			if slices.Contains(ends, from[j]) {
				panic(fmt.Sprintf("machine %s: %s leaves the terminal state %s", name, on, from[j]))
			}
			if taken[on+"\x00"+from[j]] {
				panic(fmt.Sprintf("machine %s: %s is taken twice from %s", name, on, from[j]))
			}
			taken[on+"\x00"+from[j]] = true
		}
		moves[i] = fsm.EventDesc{Name: on, Src: from, Dst: m.spell(name, edge.To)}
	}
	m.requireReached(name, initial.String(), edges)
	m.trace = telemetry.Track(name, ends...)
	callbacks := fsm.Callbacks{"after_event": func(ctx context.Context, e *fsm.Event) { m.trace.Moved(ctx, e.Event, e.Src, e.Dst) }}
	for e, run := range after {
		if _, ok := events[e.String()]; !ok {
			// The engine would drop it without a word.
			panic(fmt.Sprintf("machine %s: no edge takes %s, so nothing would run after it", name, e))
		}
		callbacks["after_"+e.String()] = func(ctx context.Context, _ *fsm.Event) { run(ctx) }
	}
	m.fsm = fsm.NewFSM(initial.String(), moves, callbacks)
	return m
}

// spell records s under its spelling, the one a state keeps for as long as the machine lives.
func (m *Machine[S, E]) spell(name string, s S) string {
	if known, ok := m.states[s.String()]; ok && known != s {
		panic(fmt.Sprintf("machine %s: two states are spelled %q", name, s))
	}
	m.states[s.String()] = s
	return s.String()
}

// requireReached panics on a state some edge or terminal list names that no chain of events reaches from the initial state.
func (m *Machine[S, E]) requireReached(name, initial string, edges []Edge[S, E]) {
	reached := map[string]bool{initial: true}
	for grew := true; grew; {
		grew = false
		for _, edge := range edges {
			if reached[edge.To.String()] {
				continue
			}
			for _, from := range edge.From {
				if reached[from.String()] {
					reached[edge.To.String()] = true
					grew = true
					break
				}
			}
		}
	}
	for spelled := range m.states {
		if !reached[spelled] {
			panic(fmt.Sprintf("machine %s: no event reaches the state %s", name, spelled))
		}
	}
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

// Take takes e where the state takes it and says whether it did, for a source that repeats what the machine has moved past.
func (m *Machine[S, E]) Take(ctx context.Context, e E) bool {
	err := m.Fire(ctx, e)
	if errors.Is(err, ErrRefused) {
		return false
	}
	if err != nil {
		slog.WarnContext(ctx, "a state machine did not take an event its state takes", "state", m.State(), "event", e, "error", err)
		return false
	}
	return true
}

// FireOrFail takes e, and where the state does not take it takes fail instead, so the machine stops its normal way; it returns what went wrong.
func (m *Machine[S, E]) FireOrFail(ctx context.Context, e, fail E) error {
	err := m.Fire(ctx, e)
	if err != nil && m.Can(fail) {
		err = errors.Join(err, m.Fire(ctx, fail))
	}
	return err
}

func (m *Machine[S, E]) Is(s S) bool  { return m.fsm.Is(s.String()) }
func (m *Machine[S, E]) Can(e E) bool { return m.fsm.Can(e.String()) }
func (m *Machine[S, E]) State() S     { return m.states[m.fsm.Current()] }

// End ends the trace of a machine its owner is done with before any terminal state.
func (m *Machine[S, E]) End(ctx context.Context) { m.trace.End(ctx) }
