package picker

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// Esc must not return a device as the selection.
func TestEscDoesNotQuit(t *testing.T) {
	m := newModel(t.Context(), func(context.Context) []*castorv1.Device { return nil }, "")
	tm, _ := m.Update(devicesDoneMsg{devices: []*castorv1.Device{{Name: "Living room", Type: "dlna", Address: "10.0.0.2"}}})
	m = tm.(model)

	tm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = tm.(model)
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("esc quit the device picker (list quit binding leaked through)")
		}
	}
	if m.selected != nil {
		t.Fatalf("esc selected %+v", m.selected)
	}
}

// q leaves at once, asking nothing, and selects nothing.
func TestQQuitsWithoutSelecting(t *testing.T) {
	m := newModel(t.Context(), func(context.Context) []*castorv1.Device { return nil }, "")
	tm, _ := m.Update(devicesDoneMsg{devices: []*castorv1.Device{{Name: "Living room", Type: "dlna", Address: "10.0.0.2"}}})
	tm, cmd := tm.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q asked before quitting")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatal("q did not quit")
	}
	if m := tm.(model); m.selected != nil {
		t.Errorf("q left %+v selected, want nothing", m.selected)
	}
}

func TestTheConfiguredDeviceIsPreselectedAndEnterCastsToIt(t *testing.T) {
	bedroom := &castorv1.Device{Name: "Bedroom", Type: "chromecast", Address: "10.0.0.9"}
	discover := func(context.Context) []*castorv1.Device {
		return []*castorv1.Device{{Name: "Living room", Type: "dlna", Address: "10.0.0.2"}, bedroom}
	}
	tm := teatest.NewTestModel(t, newModel(t.Context(), discover, "Bedroom"),
		teatest.WithInitialTermSize(80, 20),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.Ascii)),
	)
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte("Bedroom")) }, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	final := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(model)
	if final.selected != bedroom {
		t.Errorf("selected %+v, want the configured %+v", final.selected, bedroom)
	}
}
