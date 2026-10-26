package health

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestVerdicts(t *testing.T) {
	past := StallWindow + time.Second
	for _, tt := range []struct {
		name   string
		phase  Phase
		vitals Vitals
		want   Kind
	}{
		{"a cushion of media opens a cast", Reading, Vitals{Landed: 1, Position: readCushion, rate: 1, fill: 1}, ready},
		{"a cushion delivered under playback pace opens a cast too, which a pause meets later", Reading, Vitals{Landed: 1, Position: readCushion, rate: 0.8, fill: 0.8, sinceDeficit: 2 * deliverBudget}, ready},
		{"a cushion landed under half of playback pace waits, however fast its last stretch", Reading, Vitals{Landed: 1, Position: readCushion, rate: 1.5, fill: 0.4}, starting},
		{"a cushion landed under half of playback pace is given up after the deliver budget", Reading, Vitals{Landed: 1, Position: readCushion, rate: 0.4, fill: 0.4, sinceDeficit: deliverBudget + time.Second}, Undeliverable},
		{"a buffer under the cushion waits", Reading, Vitals{Landed: 1 << 20, Position: readCushion - time.Second, rate: 3}, starting},
		{"an empty buffer waits", Reading, Vitals{}, starting},
		{"a burn-in waits for the transcription lead", Reading, Vitals{Landed: 4 << 20, Position: readCushion, rate: 2, subtitles: true, lead: transcriptionLead.Seconds() - 1}, starting},
		{"the transcription lead opens the gate", Reading, Vitals{Landed: 1, Position: readCushion, rate: 1, fill: 1, subtitles: true, lead: transcriptionLead.Seconds()}, ready},
		{"a finished transcription over an empty buffer does not open", Reading, Vitals{subtitles: true, leadDone: true}, starting},
		{"an ended read is never held, even empty under burn-in", Reading, Vitals{ended: true, subtitles: true}, ready},
		{"a read under playback pace for the deliver budget is undeliverable", Reading, Vitals{Landed: 1 << 20, Position: readCushion - time.Second, rate: 0.7, sinceDeficit: deliverBudget + time.Second}, Undeliverable},
		{"a read under pace within the budget still waits", Reading, Vitals{Landed: 1 << 20, Position: readCushion - time.Second, rate: 0.7, sinceDeficit: deliverBudget - time.Second}, starting},
		{"a finished read is never undeliverable", Reading, Vitals{ended: true, sinceDeficit: 10 * deliverBudget}, ready},
		{"a read that failed before playback is dead", Reading, Vitals{Landed: 1 << 20, ended: true, failed: true}, Dead},
		{"the artifact appearing opens a delivery", Opening, Vitals{Landed: 1}, ready},
		{"a delivery without its artifact waits", Opening, Vitals{}, starting},
		{"a delivery past its patience proceeds", Opening, Vitals{overdue: true}, ready},
		{"a producer that ended with no artifact is dead", Opening, Vitals{ended: true}, Dead},
		{"a producer silent past the stall window with no artifact is stalled", Opening, Vitals{sinceGrowth: past}, Stalled},
		{"a delivery whose artifact landed is not stalled by later quiet", Opening, Vitals{Landed: 1, sinceGrowth: past}, ready},
		{"a playing cast with nothing against it is healthy", Playing, Vitals{Landed: 1 << 20, handed: 1 << 20, Position: 5 * time.Minute, played: time.Minute, rate: 2}, healthy},
		{"a device handed nothing past the fetch window is unfetched", Playing, Vitals{Landed: 8 << 20, sinceFetch: fetchWindow + time.Second, Position: 30 * time.Minute}, Unfetched},
		{"a device that fetched and went quiet is not a verdict", Playing, Vitals{Landed: 8 << 20, handed: 4 << 20, sinceFetch: past, Position: time.Hour}, healthy},
		{"a silent producer while an unholdable device has media is not stalled", Playing, Vitals{Landed: 8 << 20, handed: 4 << 20, sinceGrowth: past, Position: 45 * time.Minute, played: 15 * time.Minute}, healthy},
		{"a silent producer once an unholdable device played it out is stalled", Playing, Vitals{Landed: 8 << 20, handed: 4 << 20, sinceGrowth: past, Position: 15 * time.Minute, played: 16 * time.Minute}, Stalled},
		{"a holdable device about to outrun its read is lean", Playing, Vitals{holdable: true, Landed: 8 << 20, handed: 4 << 20, Position: 10 * time.Minute, played: 10*time.Minute - lowWater + time.Second}, Lean},
		{"a holdable device with the cushion ahead is healthy", Playing, Vitals{holdable: true, Landed: 8 << 20, handed: 4 << 20, Position: 10 * time.Minute, played: 9 * time.Minute}, healthy},
		{"a holdable device that fetched nothing yet is not held", Playing, Vitals{holdable: true, Landed: 8 << 20, Position: 10 * time.Minute, played: 10 * time.Minute}, healthy},
		{"a holdable device outrunning a finished read is left to play out", Playing, Vitals{holdable: true, ended: true, Landed: 8 << 20, handed: 4 << 20, Position: 10 * time.Minute, played: 10 * time.Minute}, healthy},
		{"a held device whose read is ahead again is replenished", Playing, Vitals{holdable: true, paused: true, Landed: 8 << 20, handed: 4 << 20, Position: 10 * time.Minute, played: 10*time.Minute - highWater}, Replenished},
		{"a held device whose read finished is replenished", Playing, Vitals{holdable: true, paused: true, ended: true, Landed: 8 << 20, handed: 4 << 20, Position: 10 * time.Minute, played: 10 * time.Minute}, Replenished},
		{"a held device still short of the cushion stays held", Playing, Vitals{holdable: true, paused: true, Landed: 8 << 20, handed: 4 << 20, Position: 10 * time.Minute, played: 10*time.Minute - highWater + time.Second, sincePause: pauseBudget - time.Second}, healthy},
		{"a device taking half of playback that took less than it has played, with the read far ahead, is choked", Playing, choking(400_000, 60*time.Second, 62*time.Second, 3*time.Minute), Choked},
		{"a device taking half of playback with a thin but positive buffer is not choked, it plays what it takes", Playing, choking(400_000, 60*time.Second, 58*time.Second, 3*time.Minute), healthy},
		{"a device whose playhead keeps real time is not choked, whatever the bytes say", Playing, func() Vitals {
			v := choking(400_000, 60*time.Second, 62*time.Second, 3*time.Minute)
			v.paceKnown, v.pace = true, 1.0
			return v
		}(), healthy},
		{"a device whose playhead falls behind real time is choked", Playing, func() Vitals {
			v := choking(800_000, 90*time.Second, 62*time.Second, 3*time.Minute)
			v.paceKnown, v.pace = true, 0.5
			return v
		}(), Choked},
		{"a device keeping real time is lifted whatever the bytes say", Playing, func() Vitals {
			v := roomy()
			v.took = v.played - time.Minute
			v.paceKnown, v.pace = true, 1.0
			return v
		}(), Roomy},
		{"a device taking exactly playback pace is not choked, whatever its buffer", Playing, choking(800_000, 60*time.Second, 58*time.Second, 3*time.Minute), healthy},
		{"a device taking less than playback with a deep buffer is not choked yet", Playing, choking(400_000, 90*time.Second, 58*time.Second, 3*time.Minute), healthy},
		{"a device taking less than playback of a read that has made little more is not choked, the read is the limit", Playing, choking(400_000, 60*time.Second, 58*time.Second, 70*time.Second), healthy},
		{"a delivery that cannot change is left to play out over a link that falls short", Playing, func() Vitals {
			v := choking(400_000, 60*time.Second, 58*time.Second, 3*time.Minute)
			v.switchable = false
			return v
		}(), healthy},
		{"a heavier rate on trial that the device takes less of than it weighs is choked, however deep its buffer", Playing, func() Vitals {
			v := choking(400_000, 90*time.Second, 58*time.Second, 3*time.Minute)
			v.trying = true
			return v
		}(), Choked},
		{"a heavier rate on trial that the device keeps up with is left to run", Playing, func() Vitals {
			v := choking(800_000, 60*time.Second, 58*time.Second, 3*time.Minute)
			v.trying = true
			return v
		}(), healthy},
		{"a cast just changed is left alone until the device is measured over the new rate", Playing, func() Vitals {
			v := choking(400_000, 60*time.Second, 58*time.Second, 3*time.Minute)
			v.sinceChange = settle - time.Second
			return v
		}(), healthy},
		{"a cast that has run well with room above it is roomy", Playing, roomy(), Roomy},
		{"a cast with no room above it is left as it is", Playing, func() Vitals { v := roomy(); v.room = false; return v }(), healthy},
		{"a cast that has not run well long enough is not lifted", Playing, func() Vitals { v := roomy(); v.sinceChange = liftAfter - time.Second; return v }(), healthy},
		{"a cast whose device took less than it played is not lifted", Playing, func() Vitals { v := roomy(); v.took = v.played - time.Second; return v }(), healthy},
		{"a cast whose device keeps a thin buffer is lifted", Playing, func() Vitals { v := roomy(); v.took = v.played + time.Second; return v }(), Roomy},
		{"a held device is released, not lifted", Playing, func() Vitals { v := roomy(); v.paused = true; v.sincePause = time.Second; return v }(), Replenished},
		{"an intake the span does not cover yet is no verdict", Playing, Vitals{holdable: true, Landed: 8 << 20, handed: 48_000_000, weight: 800_000, Position: 3 * time.Minute, played: 58 * time.Second, intake: 1}, healthy},
		{"a device held longer than it played, past the crawl mark, is starved", Playing, Vitals{holdable: true, paused: true, Landed: 8 << 20, handed: 4 << 20, Position: 3 * time.Minute, played: crawlAfter, held: crawlAfter + time.Second, sincePause: time.Second}, Starved},
		{"a device held longer than it played before the crawl mark is one outage, not a crawl", Playing, Vitals{holdable: true, paused: true, Landed: 8 << 20, handed: 4 << 20, Position: time.Minute, played: time.Minute, held: 70 * time.Second, sincePause: time.Second}, healthy},
		{"a device held past the pause budget is starved", Playing, Vitals{holdable: true, paused: true, Landed: 8 << 20, handed: 4 << 20, Position: 10 * time.Minute, played: 10 * time.Minute, sincePause: pauseBudget + time.Second}, Starved},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rule, _, err := judge(tt.phase, tt.vitals)
			if err != nil {
				t.Fatalf("judge: %v", err)
			}
			if rule.kind != tt.want {
				t.Errorf("verdict = %s (rule %q), want %s", rule.kind, rule.name, tt.want)
			}
		})
	}
}

// choking is a playing cast whose device took took of media at intake bytes a second, weighing 800 kB a second, having played played of it.
func choking(intake float64, took, played, position time.Duration) Vitals {
	return Vitals{
		holdable: true, switchable: true, sinceChange: settle, Landed: 8 << 20, weight: 800_000, took: took,
		intake: intake, intakeKnown: true, Position: position, played: played, handed: 1 << 20,
	}
}

// roomy is a playing cast that has run well at a rate with room above it.
func roomy() Vitals {
	v := choking(800_000, 60*time.Second, 50*time.Second, 3*time.Minute)
	v.room, v.sinceChange = true, liftAfter
	return v
}

func TestACastNotLiftedSaysWhy(t *testing.T) {
	for _, tt := range []struct {
		name         string
		change       func(*Vitals)
		want, detail string
	}{
		{"nothing in the way", func(*Vitals) {}, "", ""},
		{"a delivery that cannot change", func(v *Vitals) { v.switchable = false }, "the delivery cannot change while the device plays it", ""},
		{"a held device", func(v *Vitals) { v.paused = true }, "the device is held", ""},
		{"an intake not covered yet", func(v *Vitals) { v.intakeKnown = false }, "what the device takes is not measured yet", ""},
		{"a device taking less than the cast weighs", func(v *Vitals) { v.intake = 500_000 }, "the device takes less than the cast weighs", "4.0 Mbit/s of 6.4 Mbit/s"},
		{"a device with little buffered still has room", func(v *Vitals) { v.took = v.played + time.Second }, "", ""},
		{"a device that took less than it played", func(v *Vitals) { v.took = v.played - time.Second }, "the device has taken less than it has played", "-1s"},
		{"a device that has stalled for minutes", func(v *Vitals) { v.took = v.played - 2*time.Minute }, "the device has taken less than it has played", "-2m0s"},
		{"no room above", func(v *Vitals) { v.room = false }, noRoom, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := roomy()
			tt.change(&v)
			if got, detail := v.unlifted(); got != tt.want || detail != tt.detail {
				t.Errorf("unlifted = %q (%q), want %q (%q)", got, detail, tt.want, tt.detail)
			}
		})
	}
}

type said struct {
	mu      sync.Mutex
	records []slog.Record
}

func (s *said) Enabled(context.Context, slog.Level) bool { return true }
func (s *said) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
	return nil
}
func (s *said) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *said) WithGroup(string) slog.Handler      { return s }

func TestAReasonThatHoldsIsSaidOnceHoweverItsMeasurementMoves(t *testing.T) {
	records := &said{}
	previous := slog.Default()
	slog.SetDefault(slog.New(records))
	t.Cleanup(func() { slog.SetDefault(previous) })
	o := &Observer{m: Monitor{Subject: "the playing cast"}, asked: newPacing(0)}
	o.asked.at = 1_200_000

	for _, behind := range []time.Duration{2 * time.Minute, 2*time.Minute + time.Second, 119 * time.Second} {
		v := roomy()
		v.sinceChange = liftAfter
		v.took = v.played - behind
		o.explain(t.Context(), v)
	}
	// Then another reason.
	v := roomy()
	v.sinceChange, v.paused = liftAfter, true
	o.explain(t.Context(), v)

	if len(records.records) != 2 {
		t.Fatalf("said %d times, want once for the thin buffer however its measurement moved and once for the hold", len(records.records))
	}
}

func TestWhereTheViewerGotToIsTheDevicesWordElseTheClock(t *testing.T) {
	if got := (Vitals{played: time.Minute}).Reached(); got != time.Minute {
		t.Errorf("by the clock, reached %v, want the minute played", got)
	}
	if got := (Vitals{played: time.Minute, head: 40 * time.Second, headKnown: true}).Reached(); got != 40*time.Second {
		t.Errorf("reached %v, want where the device says it is, since it buffered part of that minute", got)
	}
}
