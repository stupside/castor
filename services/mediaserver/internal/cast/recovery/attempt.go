package recovery

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Intent is what a cast brings to its attempts; it never changes while they run.
type Intent struct {
	// Candidates are the ranked links, each resolved when an attempt first reaches it.
	Candidates []*source.Stream

	// Deadline is the mid-read stall bound every fetch plan is derived with.
	Deadline time.Duration

	// Start is where the viewer asked the cast to begin.
	Start time.Duration

	// Delivery seeds the first attempt; recovery may change it.
	Delivery compose.DeliveryPreference

	// Ceiling is the most the device may be asked to take, zero for none; it bounds the rung a link is read at.
	Ceiling media.Bitrate

	// Turns hears each attempt and revision.
	Turns Turns
}

// Turns is told the turns a cast takes, as they are taken.
type Turns interface {
	Attempting(try int)
	Revising(action Action, why string)
}

// Attempt is one fully decided try: link, rung, fetch terms, delivery preference.
type Attempt struct {
	// Try counts from 1, so two tries of the same terms still read apart.
	Try int

	// candidate indexes Intent.Candidates, Stream is that link, and Program is what was resolved for it.
	candidate int
	Stream    *source.Stream
	Program   media.Program

	// Origin travels with candidate, since one link's facts do not hold for another.
	Origin source.Origin

	// Rendition is the rung Program reads; zero means unknown, not unconstrained.
	Rendition source.Rendition

	// Fetch is one value, so every reader of an input reads it on the same terms.
	Fetch fetch.Plan

	// Decode only ever gains axes, so recovery strictly descends.
	Decode media.Axes

	// Delivery is carried per attempt because recovery may change it.
	Delivery compose.DeliveryPreference
}

// identity is exactly the fields a strategy can change, which is the ledger's key; Try is left out.
type identity struct {
	candidate int
	program   string
	start     time.Duration
	bitrate   media.Bitrate
	height    int
	fetch     string
	delivery  compose.DeliveryPreference
	decode    media.Axes
}

// identify reads unset fields as their defaults.
func (a Attempt) identify() identity {
	return identity{
		candidate: a.candidate,
		program:   programIdentity(a.Program),
		start:     a.Program.Start,
		bitrate:   a.Rendition.Bitrate,
		height:    a.Rendition.Height,
		fetch:     fmt.Sprint(a.Fetch),
		delivery:  cmp.Or(a.Delivery, compose.DeliveryAuto),
		decode:    a.Decode,
	}
}

// programIdentity covers every input and track, so a change to any of them is a new attempt.
func programIdentity(program media.Program) string {
	parts := make([]string, 0, len(program.Inputs)+len(program.Tracks)+len(program.Offsets)+1)
	for _, input := range program.Inputs {
		parts = append(parts, fmt.Sprintf("input:%s=%s#%s:%s:%t",
			input.ID, input.URL, input.Representation, input.ContentType, input.RequiresRelaxedInput))
	}
	for _, track := range program.Tracks {
		parts = append(parts, fmt.Sprintf("track:%s=%s:%d:%t",
			track.Kind, track.Input, track.Index, track.Optional))
	}
	offsets := make([]string, 0, len(program.Offsets))
	for input, offset := range program.Offsets {
		offsets = append(offsets, fmt.Sprintf("%s=%s", input, offset))
	}
	slices.Sort(offsets)
	parts = append(parts, fmt.Sprintf("sync:%s:%s:%s",
		program.ClockInput, program.EndPolicy, strings.Join(offsets, ",")))
	return strings.Join(parts, "|")
}

// at begins a at pos; a live source, or a position past its end, has no such place.
func (a Attempt) at(pos time.Duration) (Attempt, bool) {
	a.Program.Start = pos
	return a, pos <= 0 || !a.live() && (a.Origin.Duration == 0 || pos < a.Origin.Duration)
}

func (a Attempt) live() bool {
	return slices.ContainsFunc(a.Program.Inputs, func(input media.Input) bool { return input.Fetching().Live })
}

// reading binds a resolution to the attempt with a fetch plan derived for its program.
func (a Attempt) reading(r source.Resolution, deadline time.Duration) Attempt {
	a.Program, a.Origin, a.Rendition = r.Program, r.Origin, r.Rendition
	a.Fetch = fetch.ForProgram(a.Program, deadline)
	return a
}

// SelfFetchRate is the heaviest stream a device fetching a's URL for itself could choose, as the source declared it; zero where it declared none.
func (a Attempt) SelfFetchRate() media.Bitrate {
	return selfFetched(a, func(r source.Rendition) media.Bitrate { return r.Bitrate })
}

// SelfFetchHeight is the tallest picture a device fetching a's URL for itself could choose.
func (a Attempt) SelfFetchHeight() int {
	return cmp.Or(selfFetched(a, func(r source.Rendition) int { return r.Height }), a.Program.MeasuredHeight())
}

// selfFetched is the most of a rung's measure a device fetching a's URL could choose; a rung with a URL of its own pins the device to it.
func selfFetched[T cmp.Ordered](a Attempt, measure func(source.Rendition) T) T {
	if a.Rendition.URL != nil {
		return measure(a.Rendition)
	}
	var most T
	for _, rung := range a.Origin.Renditions {
		most = max(most, measure(rung))
	}
	return cmp.Or(most, measure(a.Rendition))
}
