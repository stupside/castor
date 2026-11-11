// Package tui runs castor's terminal programs.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// Run runs m until it quits or ctx ends; the process owns the signals, and an end of ctx is reported as its cause.
func Run(ctx context.Context, m tea.Model) (tea.Model, error) {
	// bubbletea's kill path races its input reader when started on a done context.
	if ctx.Err() != nil {
		return m, context.Cause(ctx)
	}
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	// A program killed by ctx says so beside ctx's error; the cause alone is the stop the process knows.
	if ctx.Err() != nil {
		return final, context.Cause(ctx)
	}
	return final, err
}
