package recovery

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Intent is what a cast brings to its attempts; it never changes while they run.
type Intent struct {
	// Candidates are the ranked links, each resolved when an attempt first reaches it.
	Candidates []*source.Stream

	// Deadline is the mid-read stall bound every fetch plan is derived with.
	Deadline time.Duration

	// Delivery seeds the first attempt; recovery may change it.
	Delivery compose.DeliveryPreference

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

	// candidate indexes Intent.Candidates; Program is what was resolved for it.
	candidate int
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

// identity is exactly the fields a strategy can change, for both the log and the ledger.
type identity struct {
	candidate int
	program   string
	bitrate   media.Bitrate
	height    int
	fetch     string
	delivery  compose.DeliveryPreference
	decode    media.Axes
}

// identify reads unset fields as their defaults.
func (a Attempt) identify(redactURLSecrets bool) identity {
	return identity{
		candidate: a.candidate,
		program:   programIdentity(a.Program, redactURLSecrets),
		bitrate:   a.Rendition.Bitrate,
		height:    a.Rendition.Height,
		fetch:     cmp.Or(a.Fetch.String(), "unset"),
		delivery:  cmp.Or(a.Delivery, compose.DeliveryAuto),
		decode:    a.Decode,
	}
}

// String is the ledger's key too, so a field left out of it is a change the ledger misses.
func (id identity) String() string {
	return fmt.Sprintf("candidate=%d program=%s rung_bitrate=%d rung_height=%d fetch=%s delivery=%s decode=%s",
		id.candidate, id.program, id.bitrate, id.height, id.fetch, id.delivery, id.decode)
}

// String redacts the URL secrets, because a line a user reads is not a place to print a signed query.
func (a Attempt) String() string { return a.identify(true).String() }

// programIdentity covers every input and track, so a change to any of them is a new attempt.
func programIdentity(program media.Program, redactURLSecrets bool) string {
	parts := make([]string, 0, len(program.Inputs)+len(program.Tracks)+len(program.Offsets)+1)
	for _, input := range program.Inputs {
		inputURL := input.URL.String()
		if redactURLSecrets {
			u := input.URL.Clone()
			u.User, u.RawQuery, u.Fragment = nil, "", ""
			inputURL = u.String()
		}
		parts = append(parts, fmt.Sprintf("input:%s=%s#%s:%s:%t",
			input.ID, inputURL, input.Representation, input.ContentType, input.RequiresRelaxedInput))
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

// key leaves Try out: the same link on the same terms is the same attempt.
func (a Attempt) key() string { return a.identify(false).String() }

// reading binds a resolution to the attempt with a fetch plan derived for its program.
func (a Attempt) reading(r source.Resolution, deadline time.Duration) Attempt {
	a.Program, a.Origin, a.Rendition = r.Program, r.Origin, r.Rendition
	a.Fetch = fetch.ForProgram(a.Program, deadline)
	return a
}

// SelfFetchHeight is the tallest picture a device fetching a's URL for itself could choose.
func (a Attempt) SelfFetchHeight() int {
	// A rung with a URL of its own was narrowed at the URL, so the device is pinned to it.
	if a.Rendition.URL != nil {
		return cmp.Or(a.Rendition.Height, a.Program.MeasuredHeight())
	}
	tallest := 0
	for _, rung := range a.Origin.Renditions {
		tallest = max(tallest, rung.Height)
	}
	return cmp.Or(tallest, a.Rendition.Height, a.Program.MeasuredHeight())
}
