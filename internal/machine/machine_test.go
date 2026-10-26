package machine_test

import (
	"context"
	"errors"
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
