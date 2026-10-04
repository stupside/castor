package suite

import (
	"cmp"
	"fmt"
	"math"
	"os"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/command"
	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/judge/outcome"
	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/receiver"
	"github.com/stupside/castor/e2e/receiver/viewer"
	"github.com/stupside/castor/e2e/settings"
	"github.com/stupside/castor/e2e/strategy"
)

// spec is one file in cases/.
type spec struct {
	Stream  origin.Spec      `yaml:"stream"`
	Origin  strategy.Choices `yaml:"origin"`
	Command strategy.Choice  `yaml:"command"`
	// Receiver is the device castor casts to and the person watching it.
	Receiver struct {
		Device strategy.Choice `yaml:"device"`
		Viewer strategy.Choice `yaml:"viewer"`
	} `yaml:"receiver"`
	// Castor is castor's own config.yaml for this cast; the device section is the receiver's.
	Castor yaml.Node `yaml:"castor"`
	// Config is how that config reaches castor: a file (the default) or the environment alone.
	Config string `yaml:"config"`
	// Topology is one command (the default), three services apart, or both.
	Topology []string         `yaml:"topology"`
	Outcome  string           `yaml:"outcome"`
	Expect   strategy.Choices `yaml:"expect"`
}

// plan is a spec with every name it holds bound to its strategy.
type plan struct {
	stream     origin.Stream
	behaviours []origin.Behaviour
	command    command.Command
	device     receiver.Device
	viewer     receiver.Viewer
	castor     yaml.Node
	carrier    settings.Carrier
	ceiling    int
	outcome    judge.Outcome
	checks     []judge.Check
}

// castorKnobs are the settings in a case's castor section the judge holds castor to.
type castorKnobs struct {
	Cast struct {
		MaxHeight int `yaml:"max_height"`
	} `yaml:"cast"`
}

func (s spec) resolve() (plan, error) {
	var p plan
	var err error
	if p.stream, err = catalog.Resolve(s.Stream); err != nil {
		return plan{}, err
	}
	for _, c := range s.Origin {
		b, err := strategy.Build(behaviours, c)
		if err != nil {
			return plan{}, fmt.Errorf("origin: %w", err)
		}
		p.behaviours = append(p.behaviours, b)
	}
	s.Command.Name = cmp.Or(s.Command.Name, command.URL{}.Name())
	if p.command, err = strategy.Build(commands, s.Command); err != nil {
		return plan{}, fmt.Errorf("command: %w", err)
	}
	if p.device, err = strategy.Build(families, s.Receiver.Device); err != nil {
		return plan{}, fmt.Errorf("receiver.device: %w", err)
	}
	s.Receiver.Viewer.Name = cmp.Or(s.Receiver.Viewer.Name, viewer.ToTheEnd{}.Name())
	if p.viewer, err = strategy.Build(viewers, s.Receiver.Viewer); err != nil {
		return plan{}, fmt.Errorf("receiver.viewer: %w", err)
	}
	var knobs castorKnobs
	if err := decodeSection(s.Castor, &knobs); err != nil {
		return plan{}, fmt.Errorf("castor: %w", err)
	}
	p.castor, p.ceiling = s.Castor, cmp.Or(knobs.Cast.MaxHeight, math.MaxInt)
	if p.carrier, err = carriers.Lookup(cmp.Or(s.Config, settings.File{}.Name())); err != nil {
		return plan{}, fmt.Errorf("config: %w", err)
	}
	if p.outcome, err = outcomes.Lookup(cmp.Or(s.Outcome, outcome.Plays{}.Name())); err != nil {
		return plan{}, fmt.Errorf("outcome: %w", err)
	}
	var expected []judge.Check
	for _, c := range s.Expect {
		check, err := strategy.Build(expectations, c)
		if err != nil {
			return plan{}, fmt.Errorf("expect: %w", err)
		}
		expected = append(expected, check)
	}
	p.checks, err = p.outcome.Scope(invariants, expected)
	return p, err
}

// read decodes a case strictly, so a mistyped key fails the case instead of being ignored.
func read(t *testing.T, file string) spec {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var s spec
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return s
}

// layouts are the topologies the case casts under.
func (s spec) layouts() ([]topology, error) {
	names := s.Topology
	if len(names) == 0 {
		names = []string{embedded{}.Name()}
	}
	layouts := make([]topology, len(names))
	for i, name := range names {
		var err error
		if layouts[i], err = topologies.Lookup(name); err != nil {
			return nil, fmt.Errorf("topology: %w", err)
		}
	}
	return layouts, nil
}
