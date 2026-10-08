package execute

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
)

// fakeLead mocks transcriber for gate testing without whisper model.
type fakeLead struct {
	latest float64
	done   bool
}

func (f fakeLead) LatestEnd() float64 { return f.latest }
func (f fakeLead) Done() bool         { return f.done }

func TestWaitForPlayableHoldsUntilTheTranscriptionLeads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Pace omitted: starving verdict would confuse the assertion.
		buf := gateFixture(t, 0)
		buf.burn = &fakeBurn{lead: &fakeLead{latest: 1}}
		if _, err := buf.reader.spool.Write(make([]byte, 4<<20)); err != nil {
			t.Fatal(err)
		}

		const held = 10 * time.Second
		ctx, cancel := context.WithTimeout(t.Context(), held)
		defer cancel()
		opened := make(chan error, 1)
		go func() {
			opened <- gate(ctx, buf)
		}()

		synctest.Sleep(held - time.Nanosecond)
		select {
		case err := <-opened:
			t.Fatalf("the gate opened ahead of the transcription's committed frontier (err %v)", err)
		default:
		}

		cancel()
		if err := <-opened; !errors.Is(err, context.Canceled) {
			t.Errorf("gate error = %v, want context.Canceled", err)
		}
	})
}

// stoppedDevice took nothing, last asked at last.
type stoppedDevice struct {
	last     time.Time
	buffered time.Duration
}

func (s stoppedDevice) Handed() (int64, time.Time) { return 0, s.last }
func (s stoppedDevice) Buffered() time.Duration    { return s.buffered }

// gateFixture is a buffered read over an empty spool, granted pace.
func gateFixture(t *testing.T, pace float64) *buffered {
	t.Helper()
	sp, err := deliver.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sp.CloseWrite(nil) })

	return &buffered{reader: &pull{
		proc:  finished(t),
		spool: sp,
		done:  make(chan struct{}),
		pace:  pace,
	}}
}
