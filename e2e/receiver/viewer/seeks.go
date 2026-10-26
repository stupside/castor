package viewer

import (
	"context"
	"errors"
	"io"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/receiver"
	"github.com/stupside/castor/e2e/strategy"
)

// Seeks builds a viewer who jumps to another position a while into playback: `seeks: {after: 8s, to: 30s}`.
type Seeks struct{}

func (Seeks) Name() string { return "seeks" }

func (Seeks) Build(settings yaml.Node) (receiver.Viewer, error) {
	var in struct {
		After time.Duration `yaml:"after"`
		To    time.Duration `yaml:"to"`
	}
	if err := strategy.Decode(settings, &in); err != nil {
		return nil, err
	}
	if in.After <= 0 || in.To <= 0 {
		return nil, errors.New("seeks: want positive durations such as {after: 8s, to: 30s}")
	}
	return jumper{after: in.After, to: in.To}, nil
}

type jumper struct{ after, to time.Duration }

var _ receiver.Jumper = jumper{}

func (jumper) Name() string { return "seeks" }

func (jumper) Watch(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
func (jumper) Stopped(context.Context) bool { return false }
func (jumper) WillStop() bool               { return false }
func (jumper) Pace(r io.Reader) io.Reader   { return r }
func (jumper) Realtime() bool               { return false }

func (j jumper) Seeks() (after, to time.Duration) { return j.after, j.to }
