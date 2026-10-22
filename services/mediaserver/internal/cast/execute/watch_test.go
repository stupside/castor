package execute

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
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
		buf := gateFixture(t)
		buf.transcript = &fakeTranscript{lead: &fakeLead{latest: 1}}
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
	last time.Time
}

func (s stoppedDevice) Handed() (int64, time.Time) { return 0, s.last }

// gateFixture is a buffered read over an empty spool.
func gateFixture(t *testing.T) *buffered {
	t.Helper()
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sp.CloseWrite(nil) })

	return &buffered{reader: &pull{
		proc:  finished(t),
		spool: sp,
		done:  make(chan struct{}),
	}}
}

// An audience read holds the health worker inside its resource until released.
type heldAudience struct {
	entered chan struct{}
	release chan struct{}
}

func (a heldAudience) Handed() (int64, time.Time) {
	close(a.entered)
	<-a.release
	return 0, time.Now()
}

func TestPlaybackJoinsTheHealthWorkerBeforeReturning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := heldAudience{entered: make(chan struct{}), release: make(chan struct{})}
		p := took(t, blocking)
		p.mech.audience = a
		p.dev = observedDevice{wait: func(context.Context) error { <-a.entered; return nil }}
		done := make(chan error, 1)
		go func() {
			_, err := superviseFixture(t.Context(), p.dev, p.delivery(), feed{})
			done <- err
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("returned while the health worker still holds delivery resources")
		default:
		}
		close(a.release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

// Exercise the coordinator's playback scope without acquiring real artifacts.
func superviseFixture(ctx context.Context, dev Device, d delivery, f feed) (bool, error) {
	c := newCoordinator(ctx, Cast{Device: dev}, recovery.Attempt{})

	c.move(inspectionStarted)
	c.move(inspectionFinished)
	c.move(planFinished)
	c.move(presentationReady)
	c.move(playbackRequested)
	c.move(playbackStarted)
	c.presentation = presentation{delivery: &d, feed: f}
	c.observePlayback()
	c.loop()
	_, err := c.finish()
	return c.delivered, err
}

// The execution worker borrows only pause/resume, and stays alive until an
// in-flight command finishes its cancellation cleanup.
type blockedPlaybackControl struct {
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
	returned bool
}

func (d *blockedPlaybackControl) Pause(ctx context.Context) error {
	close(d.entered)
	<-ctx.Done()
	close(d.canceled)
	<-d.release
	d.returned = true
	return ctx.Err()
}
func (*blockedPlaybackControl) Resume(context.Context) error { return nil }

type fetchingAudience struct{}

func (fetchingAudience) Handed() (int64, time.Time) { return 1, time.Now() }

func TestCancellationJoinsHealthControlBeforeReleasingResources(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})
		reachAttemptState(t, c.machine, playingBack)
		control := &blockedPlaybackControl{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
		c.releases.push(func() error {
			if !control.returned {
				t.Error("resource released before control command returned")
			}
			return nil
		})
		c.launch(healthEnded, func() error {
			return observeHealth(c.ctx, health.Monitor{Phase: health.Playing, Holdable: true, Audience: fetchingAudience{}}, control)
		})
		done := make(chan error, 1)
		go func() { c.loop(); _, err := c.finish(); done <- err }()
		<-control.entered
		c.stop(context.Canceled)
		<-control.canceled
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("attempt returned while Pause was still cleaning up")
		default:
		}
		close(control.release)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("result = %v", err)
		}
	})
}
