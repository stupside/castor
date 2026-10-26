package health

import (
	"cmp"
	"fmt"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Vitals is one reading of a watched cast: its measurements and terminal states.
type Vitals struct {
	// Landed is the bytes of artifact available now.
	Landed int64

	// sinceGrowth is how long no new media has arrived: Position when the producer states one, else Landed.
	sinceGrowth time.Duration

	// Position is the media made so far.
	Position time.Duration

	// Samples counts the speeds the producer stated, none before its first packet.
	Samples int

	// rate is media made per wall-clock second over the last rateSpan, which is what the producer delivers now.
	rate float64

	// fill is media made per wall-clock second since the watch began, which a burst at the start cannot flatter.
	fill float64

	// sinceDeficit is how long the rate has been under playback.
	sinceDeficit time.Duration

	// ended is a producer that finished, which never stalls since its buffer will not grow again.
	ended  bool
	failed bool

	// overdue is a fact about the delivery's own patience, not about the producer.
	overdue bool

	// subtitles is a burn-in cast; lead is how far its transcription has committed, and leadDone whether it finished.
	subtitles bool
	lead      float64
	leadDone  bool

	// handed is the bytes the device took, and sinceFetch how long since one moved.
	handed     int64
	sinceFetch time.Duration

	// weight is the bytes one second of this delivery has weighed lately; zero while unmeasured.
	weight float64

	// took is the media the device took: what its link carried, not what the read made.
	took time.Duration

	// intake is the bytes a second the device took over the last intakeSpan.
	intake      float64
	intakeKnown bool

	pace      float64
	paceKnown bool

	// began is where in the program the device's current fetch began; played is how long it has played since, less the time it was held.
	began  time.Duration
	played time.Duration
	// head is where the device says it has played to, when it says.
	head      time.Duration
	headKnown bool

	// climbAfter is how long a cast plays well before a better link is tried, zero for never.
	climbAfter time.Duration

	// holdable is a device the watch may pause; paused is one it holds, for sincePause, and held is every hold so far.
	holdable   bool
	paused     bool
	sincePause time.Duration
	held       time.Duration
	// viewerHeld is a device its viewer holds, which castor neither judges nor commands.
	viewerHeld bool

	// switchable is a delivery whose rate can change mid-play; room is that it may weigh more.
	switchable  bool
	sinceChange time.Duration
	room        bool
	// trying is a heavier rate on trial.
	trying bool
	// liftWait is how long a rate must have stood before a heavier one is tried; zero takes the usual.
	liftWait time.Duration
}

// outranked is a cast that has played well for as long as a better link is left alone.
func (h Vitals) outranked() bool {
	return h.climbAfter > 0 && h.played >= h.climbAfter && !h.paused && !h.viewerHeld && !h.trying && !h.ended
}

// at is where the device has played to by the clock.
func (h Vitals) at() time.Duration { return h.began + h.played }

// Reached is where the viewer got to in the attempt's program: the device's own word where it says, else the clock.
func (h Vitals) Reached() time.Duration {
	if h.headKnown {
		return h.head
	}
	return h.at()
}

// Intake is the rate the device took the cast at, in bits per second, zero where the span was not covered.
func (h Vitals) Intake() media.Bitrate {
	if !h.intakeKnown {
		return 0
	}
	return media.Bitrate(h.intake * 8)
}

// choking is a device whose link is short of the cast: by its own playhead where it reports one, else by the bytes it took.
func (h Vitals) choking() bool {
	if !h.switchable || h.sinceChange < settle {
		return false
	}
	if h.paceKnown {
		return h.pace < stallBelow
	}
	return h.chokingByBytes()
}

// chokingByBytes is a device taking media slower than it plays, with media waiting and less taken than played.
func (h Vitals) chokingByBytes() bool {
	if !h.intakeKnown || h.weight <= 0 || h.intake >= chokedBelow*h.weight {
		return false
	}
	// A heavier rate on trial is judged by what the device takes of it, which shows before its buffer does.
	return h.trying || h.Position-h.took >= highWater && h.took < h.at()
}

// roomy is a device that has run well at the rate it is asked for, with room to ask more.
func (h Vitals) roomy() bool {
	if !h.switchable || !h.room || h.paused || h.viewerHeld || h.sinceChange < cmp.Or(h.liftWait, liftAfter) {
		return false
	}
	if h.paceKnown {
		return h.pace >= keepsAbove
	}
	return h.intakeKnown && h.weight > 0 && h.intake >= chokedBelow*h.weight && h.took >= h.at()
}

const noRoom = "no heavier rate is allowed or known to be carried"

// unlifted is why a cast that stood long enough is not asked for more; detail is the measurement, which moves while why holds.
func (h Vitals) unlifted() (why, detail string) {
	switch {
	case !h.switchable:
		return "the delivery cannot change while the device plays it", ""
	case h.paused:
		return "the device is held", ""
	case h.paceKnown:
		return h.unliftedByPace()
	case !h.intakeKnown || h.weight <= 0:
		return "what the device takes is not measured yet", ""
	case h.intake < chokedBelow*h.weight:
		return "the device takes less than the cast weighs", fmt.Sprintf("%s of %s", h.Intake(), media.Bitrate(h.weight*8))
	case h.took < h.at():
		return "the device has taken less than it has played", (h.took - h.at()).Round(time.Second).String()
	case !h.room:
		return noRoom, ""
	}
	return "", ""
}

func (h Vitals) unliftedByPace() (why, detail string) {
	switch {
	case h.pace < keepsAbove:
		return "the device plays slower than real time", fmt.Sprintf("%.2fx", h.pace)
	case !h.room:
		return noRoom, ""
	}
	return "", ""
}

// playable reports that there is something to hand a device.
func (h Vitals) playable() bool { return h.Landed > 0 }

// ahead is the least media the device provably still has to play: what was made, less what it has played.
func (h Vitals) ahead() time.Duration { return h.Position - h.at() }

// paced is a producer delivering at least as fast as playback consumes.
func (h Vitals) paced() bool { return h.rate >= playbackRate }

// worthStarting is a cushion landed at a pace pauses can carry: a device held more than it plays is not watching anything.
func (h Vitals) worthStarting() bool { return h.cushioned() && h.fill >= minPace }

// crawling is a device held for longer than it has played, once it has played long enough for that to be the source and not one outage.
func (h Vitals) crawling() bool { return h.played >= crawlAfter && h.held > h.played }

// leads is a transcription far enough ahead, or finished.
func (h Vitals) leads() bool { return h.lead >= transcriptionLead.Seconds() || h.leadDone }

// cushioned is a buffer deep enough to ride out a source that goes quiet for a moment.
func (h Vitals) cushioned() bool { return h.Position >= readCushion }

const (
	// playbackRate is the pace a device plays at.
	playbackRate = 1.0

	// readCushion is the media a read holds before a device is started; a source that falls behind later is met by a pause, not a longer wait.
	readCushion = 15 * time.Second

	// deliverBudget is how long a read may stay under playback pace without landing the cushion before the gate gives the link up.
	deliverBudget = fetch.BackoffMax

	// lowWater is the media left to a device under which the watch holds it; highWater is where it lets it go again.
	lowWater  = 5 * time.Second
	highWater = 20 * time.Second

	// intakeSpan is how far back the rate a device takes the cast at is measured, long enough to ride out reads in bursts.
	intakeSpan = 20 * time.Second

	// chokedBelow is the share of playback a device must take for its link to count as carrying the cast.
	chokedBelow = 0.9

	// pauseBudget is how long a device is held for a read to get ahead again before the cast is given up.
	pauseBudget = 2 * fetch.BackoffMax

	// crawlAfter is how much a device plays before the share of time it was held judges the source.
	crawlAfter = 2 * time.Minute

	// transcriptionLead is how far a burn-in leads before playback starts.
	transcriptionLead = fetch.EncodeBurst + 10*time.Second

	// StallWindow outlasts two reconnect ceilings, so a reconnecting read is never called silent.
	StallWindow = 2*fetch.BackoffMax + 30*time.Second

	// fetchWindow is one reconnect ceiling: a device on the local network needs no longer.
	fetchWindow = fetch.BackoffMax

	// rateSpan is how far back the rate is measured: three segments of the longest common target duration.
	rateSpan = 30 * time.Second

	// minPace is the share of playback under which a producer counts as not growing.
	minPace = 0.5

	// pollInterval is under the producer's report period, so no sample is missed.
	pollInterval = 200 * time.Millisecond

	// reportInterval is how often a watch with nothing to decide says so.
	reportInterval = 5 * time.Second
)
