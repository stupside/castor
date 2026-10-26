package execute

import (
	"context"
	"time"
)

const (
	// lead is how far ahead of the device a delivery is made, so that a change in how it is made reaches the device within seconds.
	lead = 10 * time.Second

	// leadFloor is the least a delivery runs ahead, whatever it weighs.
	leadFloor = 2 << 20

	// gatePoll is how often a delivery that runs far enough ahead looks again.
	gatePoll = 20 * time.Millisecond

	// takeSpan is how far back the rate the device takes the delivery at is measured; a span must be half covered before it counts.
	takeSpan = 10 * time.Second

	// takeEvery is how often the bytes the device took are sampled.
	takeEvery = 250 * time.Millisecond
)

// gate holds the delivery back while it is further ahead of the device than lead.
func (f *stages) gate() error {
	defer f.withheld.Store(false)
	for {
		if f.passed.Load()-f.handed() <= f.allowance() {
			return nil
		}
		f.withheld.Store(true)
		select {
		case <-time.After(gatePoll):
		case <-f.ctx.Done():
			return context.Cause(f.ctx)
		}
	}
}

// allowance is lead's worth of bytes at the lesser of the stream's weight and the device's intake, so a cut reaches a slow link soon.
func (f *stages) allowance() int64 {
	made := f.Progress()
	span := made.Position - f.from
	if span <= 0 || made.Bytes <= 0 {
		return leadFloor
	}
	rate := float64(made.Bytes) / span.Seconds()
	if took, known := f.taking(); known {
		rate = min(rate, took)
	}
	return max(leadFloor, int64(rate*lead.Seconds()))
}

// taking is the bytes a second the device took over the last takeSpan, known once half of it is covered.
func (f *stages) taking() (rate float64, known bool) {
	f.takesMu.Lock()
	defer f.takesMu.Unlock()
	if now := time.Now(); now.Sub(f.sampled) >= takeEvery {
		f.sampled = now
		f.taken.Add(now, float64(f.handed()))
	}
	return f.taken.Rate(0.5)
}

// Withheld says the delivery is waiting for the device, so a device that took all it had been given did not outrun it.
func (f *stages) Withheld() bool { return f.withheld.Load() }
