package tui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type idle struct{}

func (idle) Init() tea.Cmd                       { return nil }
func (idle) Update(tea.Msg) (tea.Model, tea.Cmd) { return idle{}, nil }
func (idle) View() tea.View                      { return tea.NewView("") }

func TestAProgramOnAContextThatEndedReportsItsCause(t *testing.T) {
	cause := errors.New("interrupt signal received")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	if _, err := Run(ctx, idle{}); !errors.Is(err, cause) {
		t.Errorf("Run = %v, want the context's cause", err)
	}
}
