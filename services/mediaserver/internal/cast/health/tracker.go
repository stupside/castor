package health

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/stupside/castor/internal/machine"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// tracker is one Watch's derived state: how long each thing has been true.
type tracker struct {
	m     Monitor
	start time.Time

	// landed is the artifact's size, and grew when the producer last grew.
	landed int64
	grew   time.Time

	// making and made are the output when last seen growing or unfed, and when.
	making media.Progress
	made   time.Time

	// trail is the media position over the last rateSpan.
	trail media.Trail

	// kept is when the rate last kept up with playback.
	kept time.Time

	// samples counts the speeds the producer stated, not the N/A it prints before.
	sample  media.Progress
	samples int

	// fetchedAt is when the device first took a byte of its current fetch, which began at began.
	fetchedAt time.Time
	began     time.Duration
	handed    int64

	reading reading

	// curve maps bytes the delivery made to the media they hold.
	curve []made

	asked pacing

	// pausedAt and held are castor's holds, viewerAt and viewerHolds its viewer's, which count against played but never as castor's.
	pausedAt    time.Time
	held        time.Duration
	viewerAt    time.Time
	viewerHolds time.Duration
	playback    *machine.Machine[playbackState, controlEvent]

	reported time.Time
}

// reading is what the device takes, plays and is sent at the rate it is asked for, which a change of rate starts over.
type reading struct {
	// intake is the bytes taken over intakeSpan and its later half; the lower outlasts the burst that fills a device's buffer.
	intake, laterIntake media.Trail
	// weights is what the delivery made over the last intakeSpan.
	weights  []made
	playhead playTrail
}

func newReading(p Playhead) reading {
	return reading{intake: media.Trail{Span: intakeSpan}, laterIntake: media.Trail{Span: intakeSpan / 2}, playhead: newPlayTrail(p)}
}

func (r *reading) restart() {
	r.intake.Reset()
	r.laterIntake.Reset()
	r.weights = nil
	r.playhead.restart()
}

// read takes one reading of every port.
func (t *tracker) read(ctx context.Context) (Vitals, error) {
	now := time.Now()
	h := Vitals{subtitles: t.m.Lead != nil, holdable: t.m.Holdable && !t.playback.Is(pauseUnavailable)}

	if t.m.Landed != nil {
		var err error
		h.Landed, err = t.m.Landed()
		if err != nil {
			return h, err
		}
	}

	if l := t.m.Lead; l != nil {
		h.lead, h.leadDone = l.LatestEnd(), l.Done()
	}

	if tm := t.m.Telemetry; tm != nil {
		sample := tm.Progress()
		if sample != t.sample && sample.Speed > 0 {
			t.samples++
		}
		t.sample = sample
		h.Position, h.Samples = sample.Position, t.samples
		h.rate = t.pace(now, h.Position)
		if since := now.Sub(t.start); since > 0 {
			h.fill = h.Position.Seconds() / since.Seconds()
		}
	}
	if h.paced() {
		t.kept = now
	}
	h.sinceDeficit = now.Sub(t.kept)

	// Growth is media at a pace: a muxer pads bytes with no media behind them, and a trickle never goes silent.
	grew := h.Landed != t.landed
	if t.m.Telemetry != nil {
		grew = h.rate >= minPace
	}
	if grew {
		t.landed, t.grew = h.Landed, now
	}
	h.sinceGrowth = now.Sub(t.grew)

	if p := t.m.Producer; p != nil {
		select {
		case <-p.Done():
			h.ended = true
			// A terminal error is published with Done, never before.
			if tm := t.m.Telemetry; tm != nil {
				h.failed = tm.Err() != nil
			}
		default:
		}
	}

	if a := t.m.Audience; a != nil {
		handed, last := a.Handed()
		// A device that asked for the stream again was handed it afresh, even from where it was.
		if began := a.Began(); began != t.began || handed < t.handed {
			t.restarted(ctx, now, began)
			h.restarted = true
		}
		t.handed = handed
		h.handed = handed
		if handed > 0 {
			if t.fetchedAt.IsZero() {
				t.fetchedAt = now
			}
			h.intake, h.intakeKnown = t.intakeRate(now, handed)
		}
		// A device that fetched nothing yet is measured from the watch's start, not from the epoch.
		h.sinceFetch = now.Sub(cmp.Or(last, t.start))
	}
	if sv := t.m.Served; sv != nil {
		made := sv.Progress()
		h.weight = t.weigh(now, made)
		h.took = t.mediaAt(h.handed)
		// Samples behind what the device passed are of no further use.
		for len(t.curve) > 1 && t.curve[1].bytes <= h.handed {
			t.curve = t.curve[1:]
		}
		if t.m.Fed != nil {
			// Bytes see a long GOP between indexed keyframes; position sees HLS, which states no bytes.
			if made.Bytes != t.making.Bytes || made.Position != t.making.Position || !t.m.Fed() {
				t.making, t.made = made, now
			}
			h.sinceMade = now.Sub(t.made)
		}
	}
	h.switchable = t.m.Switchable
	h.sinceChange = t.asked.sinceChange(now)
	_, h.room, _ = t.asked.lift(now)
	h.liftWait, h.trying = t.asked.wait(), t.asked.onTrial()
	h.climbAfter = t.m.Climb
	// Only a device plays, and not before it first fetches: the watch starts as Play returns, which understates the media on hand.
	if t.m.Phase == Playing {
		h.playing = true
		h.paused = t.playback.Is(playbackPaused) || t.playback.Is(resumeRefused)
		head := &t.reading.playhead
		h.pace, h.paceKnown = head.measure(ctx, now, h.paused)
		h.head, h.headKnown, h.viewerHeld = head.last, head.lastKnown, head.held
		switch {
		case h.viewerHeld && t.viewerAt.IsZero():
			t.viewerAt = now
		case !h.viewerHeld && !t.viewerAt.IsZero():
			t.viewerHolds += now.Sub(t.viewerAt)
			t.viewerAt = time.Time{}
		}
		h.held = t.held
		if h.paused {
			h.sincePause = now.Sub(t.pausedAt)
			h.held += h.sincePause
		}
		stopped := h.held + t.viewerHolds
		if !t.viewerAt.IsZero() {
			stopped += now.Sub(t.viewerAt)
		}
		// With no audience to say when it began, the device is taken to play from the start.
		if t.m.Audience == nil {
			t.fetchedAt = t.start
		}
		if !t.fetchedAt.IsZero() {
			h.played = now.Sub(t.fetchedAt) - stopped
		}
		h.began = t.began
	}

	if t.m.Grace > 0 {
		h.overdue = now.Sub(t.start) > t.m.Grace
	}

	return h, nil
}

// restarted starts the measures over: what the device took and played before began says nothing of it now.
func (t *tracker) restarted(ctx context.Context, now time.Time, began time.Duration) {
	slog.InfoContext(ctx, "the device asked for the stream from elsewhere", "gate", t.m.Subject, "from", t.began, "to", began)
	t.began, t.fetchedAt, t.held, t.grew, t.kept = began, time.Time{}, 0, now, now
	if !t.pausedAt.IsZero() {
		t.pausedAt = now
	}
	t.viewerAt, t.viewerHolds = time.Time{}, 0
	t.trail.Reset()
	t.curve = nil
	t.reading.restart()
	t.reading.playhead.forget()
}

// pace records position and reports the media made per second over the trail, zero until it spans two readings.
func (t *tracker) pace(now time.Time, position time.Duration) float64 {
	t.trail.Add(now, position.Seconds())
	rate, _ := t.trail.Rate(0)
	return rate
}

// made is the media a delivery had made, and its bytes, at one instant.
type made struct {
	at       time.Time
	bytes    int64
	position time.Duration
}

// weigh records what the delivery has made and reports the bytes a second of it weighs over the last intakeSpan.
func (t *tracker) weigh(now time.Time, progress media.Progress) float64 {
	if progress.Bytes <= 0 || progress.Position <= 0 {
		return 0
	}
	sample := made{at: now, bytes: progress.Bytes, position: progress.Position}
	if n := len(t.curve); n == 0 || t.curve[n-1].bytes < sample.bytes {
		t.curve = append(t.curve, sample)
	}
	r := &t.reading
	r.weights = append(r.weights, sample)
	for len(r.weights) > 1 && now.Sub(r.weights[1].at) >= intakeSpan {
		r.weights = r.weights[1:]
	}
	oldest := r.weights[0]
	if span := sample.position - oldest.position; span >= 2*time.Second {
		return float64(sample.bytes-oldest.bytes) / span.Seconds()
	}
	return float64(sample.bytes) / sample.position.Seconds()
}

// mediaAt is the media a delivery had made by the time it had made bytes, as far as it has been seen to.
func (t *tracker) mediaAt(bytes int64) time.Duration {
	if bytes <= 0 || len(t.curve) == 0 {
		return 0
	}
	i, found := slices.BinarySearchFunc(t.curve, bytes, func(m made, b int64) int { return cmp.Compare(m.bytes, b) })
	if found {
		return t.curve[i].position
	}
	if i == len(t.curve) {
		return t.curve[i-1].position
	}
	if i == 0 {
		return 0
	}
	lo, hi := t.curve[i-1], t.curve[i]
	share := float64(bytes-lo.bytes) / float64(hi.bytes-lo.bytes)
	return lo.position + time.Duration(share*float64(hi.position-lo.position))
}

// intakeRate records what the device has taken and reports bytes per second, once most of intakeSpan is covered.
func (t *tracker) intakeRate(now time.Time, handed int64) (float64, bool) {
	r := &t.reading
	r.intake.Add(now, float64(handed))
	r.laterIntake.Add(now, float64(handed))
	rate, known := r.intake.Rate(0.75)
	if !known {
		return 0, false
	}
	if later, known := r.laterIntake.Rate(0); known {
		rate = min(rate, later)
	}
	return rate, true
}

// fault ends the watch with the producer's own error and evidence; only an abandon is logged, a revise is the recovery's to say.
func (t *tracker) fault(ctx context.Context, r rule, act action, h Vitals) error {
	f := &Fault{
		Kind:    r.kind,
		Revise:  act == revise,
		Subject: t.m.Subject,
		Vitals:  h,
	}
	if r.blames == theProducer && t.m.Producer != nil {
		if tm := t.m.Telemetry; tm != nil {
			f.Err = tm.Err()
		}
		f.Evidence = t.m.Producer.Evidence()
	}
	if act == abandon {
		slog.WarnContext(ctx, "cast abandoned", "gate", t.m.Subject, "verdict", r.kind.String(), "rule", r.name)
	}
	return f
}

// report says what the watch is waiting on.
func (t *tracker) report(ctx context.Context, r rule, h Vitals) {
	if time.Since(t.reported) < reportInterval {
		return
	}
	t.reported = time.Now()
	slog.DebugContext(ctx, "watch",
		"gate", t.m.Subject,
		"rule", r.name,
		"media_position", h.Position.Round(time.Second),
		"ahead", h.ahead().Round(time.Second),
		"paused", h.paused,
	)
}
