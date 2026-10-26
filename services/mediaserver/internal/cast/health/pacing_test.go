package health

import (
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// took is what a device taking bytesPerSecond of a cast weighing 800 kB/s looks like.
func took(bytesPerSecond float64) Vitals {
	return Vitals{intake: bytesPerSecond, intakeKnown: true, weight: 800_000}
}

func TestACastIsLightenedToAShareOfWhatTheLinkTookAndRemembersWhatItWeighedUnasked(t *testing.T) {
	p := newPacing(0)
	got, ok := p.lighten(took(500_000))
	if !ok || got.to != 3_000_000 {
		t.Fatalf("lighten = %s, %t, want 3.0 Mbit/s: three quarters of the 4.0 Mbit/s taken", got.to, ok)
	}
	if err := p.commit(t.Context(), got, time.Now()); err != nil || p.full != 6_400_000 {
		t.Errorf("the cast is remembered to weigh %s unasked, want 6.4 Mbit/s", p.full)
	}
}

func TestALinkTooPoorForACastWorthSendingIsNotAskedForLess(t *testing.T) {
	p := newPacing(0)
	if got, ok := p.lighten(took(50_000)); ok {
		t.Errorf("lighten = %s, want none under %s", got.to, lowest)
	}
	if _, ok := p.lighten(Vitals{}); ok {
		t.Error("lighten had an answer for a device whose intake was never measured")
	}
}

func TestACastAlreadyAskedForLessIsAskedForLessStill(t *testing.T) {
	p := newPacing(2_000_000)
	// A device taking more than the cast weighs cannot be what lightened it again, but the answer must still be lighter.
	got, ok := p.lighten(took(400_000))
	if !ok || got.to >= 2_000_000 {
		t.Errorf("lighten = %s, %t, want a rate under the 2.0 Mbit/s it is asked for", got.to, ok)
	}
}

// moved commits the move to to, failing t where the rate machine refuses it.
func moved(t *testing.T, p *pacing, move rateEvent, to media.Bitrate, at time.Time) {
	t.Helper()
	if err := p.commit(t.Context(), change{move: move, to: to}, at); err != nil {
		t.Fatal(err)
	}
}

func TestACastIsLiftedBetweenWhatItKeepsUpWithAndWhatItDoesNot(t *testing.T) {
	now := time.Now()
	p := newPacing(4_000_000)
	p.full = 6_400_000
	if _, ok, _ := p.lift(now); ok {
		t.Fatal("a cast at its policy was lifted")
	}

	// It dropped from 4.0 just now: the next rate is tried between the two, not a quarter on top.
	moved(t, &p, rateLightened, 2_400_000, now)
	if got, ok, _ := p.lift(now); !ok || got.to != 3_200_000 {
		t.Fatalf("lift = %s, %t, want 3.2 Mbit/s: between the 2.4 it keeps up with and the 4.0 it did not", got.to, ok)
	}
	// Once the avoided rate is forgotten, a quarter more, never past the policy.
	if got, ok, _ := p.lift(now.Add(avoid + time.Minute)); !ok || got.to != 3_000_000 {
		t.Errorf("lift = %s, %t after the avoid window, want a quarter more: 3.0 Mbit/s", got.to, ok)
	}
}

func TestAHeavierRateOnTrialStandsOnceItHeldAndIsGivenBackIfItDoesNot(t *testing.T) {
	now := time.Now()
	p := newPacing(0)
	p.at, p.full = 2_000_000, 6_400_000

	moved(t, &p, rateLifted, 2_500_000, now)
	if !p.onTrial() || p.before != 2_000_000 {
		t.Fatalf("a lift is not on trial: %+v", p)
	}
	if _, ok, _ := p.lift(now); ok {
		t.Error("a rate on trial was lifted again")
	}
	if got, ok := p.lighten(Vitals{}); !ok || got != (change{move: rateGivenBack, to: 2_000_000}) {
		t.Errorf("lighten = %+v, %t on a trial that fell short, want the old 2.0 Mbit/s given back with no measure needed", got, ok)
	}

	if err := p.settled(t.Context(), now.Add(probeFor-time.Second)); err != nil || !p.onTrial() {
		t.Errorf("the trial stood before it ran its time: %v", err)
	}
	if err := p.settled(t.Context(), now.Add(probeFor)); err != nil || !p.machine.Is(rateProven) || p.wait() != climbAfter {
		t.Errorf("the trial that held did not stand: %+v", p)
	}

	// A trial given back leaves the heavier rate avoided for twice as long, each time.
	later := now.Add(time.Hour)
	moved(t, &p, rateLifted, 3_000_000, later)
	moved(t, &p, rateGivenBack, 2_500_000, later.Add(probeFor))
	if p.bad != 3_000_000 || p.backoff != 2*avoid || !p.machine.Is(rateSteady) {
		t.Errorf("a trial given back left bad=%s backoff=%v in %s, want 3.0 Mbit/s for %v", p.bad, p.backoff, p.machine.State(), 2*avoid)
	}
	moved(t, &p, rateLifted, 3_000_000, later.Add(2*probeFor))
	moved(t, &p, rateGivenBack, 2_500_000, later.Add(3*probeFor))
	if p.backoff != 4*avoid {
		t.Errorf("a second failure avoids for %v, want %v", p.backoff, 4*avoid)
	}
	moved(t, &p, rateLifted, 3_000_000, later.Add(4*probeFor))
	moved(t, &p, rateGivenBack, 2_500_000, later.Add(5*probeFor))
	if p.backoff != avoidMost {
		t.Errorf("repeated failures avoid for %v, want no more than %v", p.backoff, avoidMost)
	}
}

func TestACastLiftedPastWhatItWeighsUnaskedIsAskedForNoBound(t *testing.T) {
	p := newPacing(0)
	p.at, p.full = 5_600_000, 6_400_000
	if got, ok, _ := p.lift(time.Now()); !ok || got.to != 0 {
		t.Errorf("lift = %s, %t, want no bound: the cast weighs less than the step", got.to, ok)
	}
	if p.sinceChange(time.Now()) < time.Hour {
		t.Error("a rate that never changed has not stood for good")
	}
}
