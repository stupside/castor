package health

import (
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

func TestWhereTheViewerGotToIsTheClock(t *testing.T) {
	if got := (Vitals{played: time.Minute}).Reached(); got != time.Minute {
		t.Errorf("by the clock, reached %v, want the minute played", got)
	}
}
