package machine_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stupside/castor/internal/machine"
)

type doorState uint8

const (
	closed doorState = iota
	open
)

func (s doorState) String() string { return [...]string{closed: "closed", open: "open"}[s] }

type doorEvent uint8

const (
	opened doorEvent = iota
	knocked
)

func (e doorEvent) String() string { return [...]string{opened: "opened", knocked: "knocked"}[e] }

func door(after map[doorEvent]func(context.Context)) *machine.Machine[doorState, doorEvent] {
	return machine.New("door", closed, nil, []machine.Edge[doorState, doorEvent]{
		{On: opened, From: []doorState{closed}, To: open},
		{On: knocked, From: []doorState{closed}, To: closed},
	}, after)
}

func TestAMachineMovesEvenUnderACancelledContextAndRefusesWhatItsStateDoesNotTake(t *testing.T) {
	ran := 0
	m := door(map[doorEvent]func(context.Context){opened: func(context.Context) { ran++ }})
	if err := m.Fire(t.Context(), knocked); err != nil || m.State() != closed {
		t.Fatalf("knocking on a closed door = %v, in %s; want it to stay closed, no error", err, m.State())
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.Fire(cancelled, opened); err != nil || m.State() != open || ran != 1 {
		t.Fatalf("opening under a cancelled context = %v, in %s, after ran %d times; want open, after run once", err, m.State(), ran)
	}
	if err := m.Fire(t.Context(), knocked); !errors.Is(err, machine.ErrRefused) || m.State() != open {
		t.Errorf("knocking on an open door = %v, in %s; want it refused, the door left open", err, m.State())
	}
}

func TestAnAfterNoEdgeTakesIsRefusedWhenTheMachineIsBuilt(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a machine was built with something to run after an event no edge takes")
		}
	}()
	machine.New("door", closed, nil, []machine.Edge[doorState, doorEvent]{{On: opened, From: []doorState{closed}, To: open}},
		map[doorEvent]func(context.Context){knocked: func(context.Context) {}})
}

func TestTakeMovesWhereTheStateTakesTheEventAndSaysWhetherItDid(t *testing.T) {
	m := door(nil)
	if !m.Take(t.Context(), opened) || m.State() != open {
		t.Fatalf("opening a closed door: in %s, want it open", m.State())
	}
	if m.Take(t.Context(), opened) || m.State() != open {
		t.Fatalf("opening an open door took it, or moved it to %s", m.State())
	}
	if !door(nil).Take(t.Context(), knocked) {
		t.Fatal("a knock a closed door takes, though it stays closed, was not taken")
	}
}

func TestATableThatCannotMeanWhatItsAuthorWroteIsRefusedWhenTheMachineIsBuilt(t *testing.T) {
	type edges = []machine.Edge[doorState, doorEvent]
	for _, tt := range []struct {
		name     string
		terminal []doorState
		edges    edges
	}{
		{"an event taken twice from one state", nil, edges{{On: opened, From: []doorState{closed}, To: open}, {On: opened, From: []doorState{closed}, To: closed}}},
		{"a way out of a terminal state", []doorState{open}, edges{{On: opened, From: []doorState{closed}, To: open}, {On: knocked, From: []doorState{open}, To: closed}}},
		{"a state no event reaches", nil, edges{{On: knocked, From: []doorState{closed}, To: closed}, {On: opened, From: []doorState{open}, To: open}}},
		{"a terminal state no event reaches", []doorState{open}, edges{{On: knocked, From: []doorState{closed}, To: closed}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("a machine was built on a table that cannot mean what its author wrote")
				}
			}()
			machine.New("door", closed, tt.terminal, tt.edges, nil)
		})
	}
}

// alike is a state whose every value is spelled the same, as two states of one machine must not be.
type alike uint8

func (alike) String() string { return "alike" }

type likewise uint8

func (likewise) String() string { return "likewise" }

func TestTwoStatesOrEventsSpelledAlikeAreRefusedWhenTheMachineIsBuilt(t *testing.T) {
	t.Run("states", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("a machine was built with two states spelled alike")
			}
		}()
		machine.New("pair", alike(0), nil, []machine.Edge[alike, likewise]{{On: 0, From: []alike{0}, To: 1}}, nil)
	})
	t.Run("events", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("a machine was built with two events spelled alike")
			}
		}()
		machine.New("pair", closed, nil, []machine.Edge[doorState, alike]{{On: 0, From: []doorState{closed}, To: open}, {On: 1, From: []doorState{open}, To: closed}}, nil)
	})
}

func TestTakeIsAtomicSoOnlyOneOfManyCallersMovesTheMachine(t *testing.T) {
	m := door(nil)
	var moved atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if m.Take(t.Context(), opened) {
				moved.Add(1)
			}
		})
	}
	wg.Wait()
	if moved.Load() != 1 || m.State() != open {
		t.Errorf("%d callers moved the door, which is %s; want exactly one, and open", moved.Load(), m.State())
	}
}

type jobState uint8

const (
	idle jobState = iota
	running
	stopped
)

func (s jobState) String() string {
	return [...]string{idle: "idle", running: "running", stopped: "stopped"}[s]
}

type jobEvent uint8

const (
	started jobEvent = iota
	finished
	failed
)

func (e jobEvent) String() string {
	return [...]string{started: "started", finished: "finished", failed: "failed"}[e]
}

func job() *machine.Machine[jobState, jobEvent] {
	return machine.New("job", idle, []jobState{stopped}, []machine.Edge[jobState, jobEvent]{
		{On: started, From: []jobState{idle}, To: running},
		{On: finished, From: []jobState{running}, To: stopped},
		{On: failed, From: []jobState{idle, running}, To: stopped},
	}, nil)
}

func TestFireOrFailStopsTheMachineItsNormalWayWhereTheEventIsRefused(t *testing.T) {
	m := job()
	if err := m.FireOrFail(t.Context(), finished, failed); !errors.Is(err, machine.ErrRefused) || m.State() != stopped {
		t.Fatalf("finishing a job that never started = %v, in %s; want the refusal reported and the job stopped", err, m.State())
	}
	if err := m.FireOrFail(t.Context(), finished, failed); !errors.Is(err, machine.ErrRefused) || m.State() != stopped {
		t.Errorf("finishing a stopped job = %v, in %s; want only the refusal, the job still stopped", err, m.State())
	}
	if err := job().FireOrFail(t.Context(), started, failed); err != nil {
		t.Errorf("starting a job = %v, want nothing wrong", err)
	}
}
