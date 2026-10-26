package media

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
)

type TrackKind string

const (
	TrackVideo TrackKind = "video"
	TrackAudio TrackKind = "audio"
)

// TrackRef selects one output track from one input (Optional = ffmpeg optional-map semantics).
type TrackRef struct {
	Input    InputID
	Kind     TrackKind
	Index    int
	Optional bool
}

type EndPolicy string

const (
	EndAtShortest EndPolicy = "shortest"
	EndAtLongest  EndPolicy = "longest"
)

// Program is a normalized castable program (inputs retain order, tracks bind to sources).
type Program struct {
	Inputs []Input
	Tracks []TrackRef
	// NeedsTrackSelection means handing the source URL to a device cannot
	// guarantee the chosen language, even if its track happens to be first.
	NeedsTrackSelection bool

	// ClockInput, Offsets, EndPolicy: how independently fetched inputs share a timeline.
	ClockInput InputID
	Offsets    map[InputID]time.Duration
	EndPolicy  EndPolicy

	// Start is where in the source the read begins.
	Start time.Duration

	observation *Observation
	declaration *programDeclaration
}

// NewProgram validates and clones so mutations of caller's slices/URLs/headers cannot change it.
func NewProgram(program Program) (Program, error) {
	program.observation, program.declaration = nil, nil
	p := program.Clone()
	if err := p.Validate(); err != nil {
		return Program{}, err
	}
	return p, nil
}

// Measurement reports a successful observation only while its original
// bindings still match this program. The returned facts are copied.
func (p *Program) Measurement() (ProbeInfo, bool) {
	if p.observation == nil {
		return ProbeInfo{}, false
	}
	return p.observation.For(*p)
}

// SetMeasurement replaces the probe facts for the program's current bindings.
func (p *Program) SetMeasurement(info ProbeInfo) {
	observed := Observe(*p, info)
	p.SetObservation(observed)
}

// SetObservation carries a probe's original bindings through ranking and
// resolution. Measurement refuses it if this program selects different media.
func (p *Program) SetObservation(observed Observation) {
	p.observation = &observed
}

// MeasuredHeight is the probed height, or zero when no measurement established one.
func (p *Program) MeasuredHeight() int {
	info, _ := p.Measurement()
	return info.VideoHeight
}

// SameBindings reports whether two programs read the same media (same resources, identities, tracks).
func (p Program) SameBindings(other Program) bool {
	if len(p.Inputs) != len(other.Inputs) {
		return false
	}
	for i, input := range p.Inputs {
		against := other.Inputs[i]
		if input.ID != against.ID || urlString(input.URL) != urlString(against.URL) ||
			input.Representation != against.Representation ||
			input.ContentType != against.ContentType ||
			!maps.EqualFunc(input.Headers, against.Headers, slices.Equal) {
			return false
		}
	}
	return slices.Equal(p.Tracks, other.Tracks) && p.ClockInput == other.ClockInput &&
		p.EndPolicy == other.EndPolicy && maps.Equal(p.Offsets, other.Offsets)
}

// urlString names a URL for comparison (nil = empty string; Validate refuses that anyway).
func urlString(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

// Validate reports malformed identities and bindings before planner/executor has to infer intent.
func (p Program) Validate() error {
	if len(p.Inputs) == 0 {
		return fmt.Errorf("media program has no inputs")
	}

	inputs := make(map[InputID]struct{}, len(p.Inputs))
	for i, input := range p.Inputs {
		if strings.TrimSpace(string(input.ID)) == "" {
			return fmt.Errorf("media input %d has no ID", i)
		}
		if _, exists := inputs[input.ID]; exists {
			return fmt.Errorf("media input ID %q is duplicated", input.ID)
		}
		if input.URL == nil {
			return fmt.Errorf("media input %q has no URL", input.ID)
		}
		inputs[input.ID] = struct{}{}
	}

	if len(p.Tracks) == 0 {
		return fmt.Errorf("media program selects no tracks")
	}
	kinds := make(map[TrackKind]struct{}, len(p.Tracks))
	for i, track := range p.Tracks {
		if _, exists := inputs[track.Input]; !exists {
			return fmt.Errorf("media track %d references unknown input %q", i, track.Input)
		}
		if !track.Kind.valid() {
			return fmt.Errorf("media track %d has invalid kind %q", i, track.Kind)
		}
		if track.Index < 0 {
			return fmt.Errorf("media track %d has negative %s index %d", i, track.Kind, track.Index)
		}
		if _, exists := kinds[track.Kind]; exists {
			return fmt.Errorf("media program selects more than one %s track", track.Kind)
		}
		kinds[track.Kind] = struct{}{}
	}

	if _, exists := inputs[p.ClockInput]; !exists {
		return fmt.Errorf("media clock references unknown input %q", p.ClockInput)
	}
	if !p.EndPolicy.valid() {
		return fmt.Errorf("media program has invalid end policy %q", p.EndPolicy)
	}
	for id := range p.Offsets {
		if _, exists := inputs[id]; !exists {
			return fmt.Errorf("media offset references unknown input %q", id)
		}
	}
	return nil
}

func (k TrackKind) valid() bool {
	switch k {
	case TrackVideo, TrackAudio:
		return true
	default:
		return false
	}
}

func (p EndPolicy) valid() bool {
	switch p {
	case EndAtShortest, EndAtLongest:
		return true
	default:
		return false
	}
}

func (p Program) LookupInput(id InputID) (Input, bool) {
	for _, input := range p.Inputs {
		if input.ID == id {
			return input, true
		}
	}
	return Input{}, false
}

// Track returns the first selected track of kind (bool distinguishes absent from zero index).
func (p Program) Track(kind TrackKind) (TrackRef, bool) {
	for _, track := range p.Tracks {
		if track.Kind == kind {
			return track, true
		}
	}
	return TrackRef{}, false
}

// TrackInput returns the selected track of kind and the position of the input carrying it.
func (p Program) TrackInput(kind TrackKind) (TrackRef, int, bool) {
	ref, ok := p.Track(kind)
	if !ok {
		return TrackRef{}, 0, false
	}
	i := slices.IndexFunc(p.Inputs, func(in Input) bool { return in.ID == ref.Input })
	return ref, i, i >= 0
}

// Seamed reports that any input's timeline may break (see Fetch.seamed).
func (p Program) Seamed() bool {
	return slices.ContainsFunc(p.Inputs, func(in Input) bool { return in.Fetch.seamed() })
}

// PrimaryInput returns the input that owns the program clock (bool for incomplete program).
func (p Program) PrimaryInput() (Input, bool) {
	return p.LookupInput(p.ClockInput)
}

// SelfFetchable reports whether one URL is sufficient for a device to fetch the complete program; a device handed one would play it from the start.
func (p Program) SelfFetchable() bool {
	if len(p.Inputs) != 1 || p.Start != 0 {
		return false
	}
	if p.NeedsTrackSelection {
		return false
	}
	if slices.ContainsFunc(p.Tracks, func(track TrackRef) bool { return track.Index != 0 }) {
		return false
	}
	input := p.Inputs[0]
	return len(input.Headers) == 0 && !input.RequiresRelaxedInput
}

// Clone returns a deep enough copy for independent planning (URL, header, offset copies).
func (p Program) Clone() Program {
	clone := Program{
		Inputs:              slices.Clone(p.Inputs),
		Tracks:              slices.Clone(p.Tracks),
		NeedsTrackSelection: p.NeedsTrackSelection,
		ClockInput:          p.ClockInput,
		Offsets:             maps.Clone(p.Offsets),
		EndPolicy:           p.EndPolicy,
		Start:               p.Start,
	}
	for i := range clone.Inputs {
		clone.Inputs[i].URL = p.Inputs[i].URL.Clone()
		clone.Inputs[i].Headers = p.Inputs[i].Headers.Clone()
	}
	// Evidence owns immutable snapshots; cloning a program preserves those
	// bindings instead of rebinding old facts to the clone's future selection.
	clone.observation, clone.declaration = p.observation, p.declaration
	return clone
}
