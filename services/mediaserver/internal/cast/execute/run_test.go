package execute

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/looplab/fsm"
	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// countedPublisher is the delivery publisher a handed-off cast must never reach.
type countedPublisher struct{ asked *atomic.Int64 }

func (a countedPublisher) Publish(context.Context, http.Handler) (output.Publication, error) {
	a.asked.Add(1)
	return nil, errors.New("this host has no local route")
}

func handoffStream() *source.Stream {
	c := &source.Stream{URL: &url.URL{Scheme: "https", Host: "cdn.example", Path: "/movie.mp4"}, ContentType: media.MP4}
	c.Probe = &media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 360, VideoBitDepth: 8, AudioCodec: media.CodecAAC, AudioChannels: 2}
	return c
}

func TestAHandoffBuildsNoLocalMachinery(t *testing.T) {
	var asked atomic.Int64
	dev := &fakeDevice{caps: chromecastLike(media.MP4), ended: true}
	got := Cast{Timelines: direct{}, Outputs: testOutputs(), MaxHeight: 1080, Device: dev, Publisher: countedPublisher{asked: &asked}}.Run(t.Context(),
		recovery.Attempt{Program: programFromStream(t, handoffStream())})
	if got.Err != nil {
		t.Fatalf("a handoff depended on local relay resources: %v", got.Err)
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("delivery listener opened %d time(s), want none for a handoff", n)
	}
	if got.Evidence.Reached != health.Delivered {
		t.Errorf("reached %s, want %s", got.Evidence.Reached, health.Delivered)
	}
}

func TestADeviceThatRefusesPlayIsBlamed(t *testing.T) {
	refused := errors.New("SOAP SetAVTransportURI: 714")
	dev := &fakeDevice{caps: chromecastLike(media.MP4), refuse: refused}
	out := realCast(dev, "", "").
		Run(t.Context(), recovery.Attempt{Try: 1, Program: programFromStream(t, handoffStream())})

	if out.Err == nil {
		t.Fatal("the cast reported success though the device refused the URL")
	}
	if !errors.Is(out.Evidence.PlayErr, refused) {
		t.Errorf("evidence carries PlayErr %v, want the device's own refusal %v", out.Evidence.PlayErr, refused)
	}
	if out.Evidence.Reached == health.Playing {
		t.Error("a cast whose device refused the URL reached Playing")
	}
}

func TestPresentationWithoutReadinessNeverReachesTheDevice(t *testing.T) {
	var started atomic.Bool
	dev := &coordinatorDevice{
		start: func(context.Context) error { started.Store(true); return nil },
		wait:  blocking,
	}
	c := newCoordinator(t.Context(), Cast{Device: dev}, recovery.Attempt{})
	reachAttemptState(t, c.machine, producing)
	c.preparation = c.launch(presentationReady, func() error {
		c.presentation = presentation{
			target:      &url.URL{Scheme: "https", Host: "example.test"},
			contentType: media.MP4, source: true,
		}
		return nil
	})
	c.loop()
	_, err := c.finish()
	if err == nil || started.Load() || c.ready != nil || !c.machine.Is(attemptFinished) {
		t.Fatalf("unproved presentation: started=%v ready=%v state=%s error=%v", started.Load(), c.ready, c.machine.Current(), err)
	}
}

// TestEveryAttemptOwnsAFreshWorkDirectoryAndLeavesNoneBehind is what makes a revised cast safe to offer.
func TestEveryAttemptOwnsAFreshWorkDirectoryAndLeavesNoneBehind(t *testing.T) {
	var dirs []string
	for range 2 {
		s := testPreparation(t.Context(), Cast{Timelines: direct{}, Outputs: testOutputs()}, recovery.Attempt{Program: programFromStream(t, handoffStream())})
		work, _, err := s.prepare()
		if err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, work)
		if _, err := os.Stat(work); err != nil {
			t.Fatal(err)
		}
		if err := s.releases.release(); err != nil {
			t.Fatal(err)
		}
	}

	if len(dirs) != 2 || dirs[0] == dirs[1] {
		t.Fatalf("two attempts ran in %v, want two different directories", dirs)
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("the abandoned attempt's directory %s is still on disk (%v)", dir, err)
		}
	}
}

// TestABufferCopiedWholeIsServedAsItIs: an encoder that would change nothing is not run, so the film is on disk once.
func TestABufferCopiedWholeIsServedAsItIs(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveFixture(t, ffmpegPath)

	dev := &fakeDevice{caps: dlnaLike(), drain: true}
	c := realCast(dev, ffmpegPath, ffprobePath)
	program := programFromStream(t, origin.stream())

	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	s := testPreparation(ctx, c, recovery.Attempt{Try: 1, Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline)})
	t.Cleanup(func() { _ = s.releases.release() })
	p, err := s.preparePresentation()
	if err == nil {
		err = hand(s.ctx, dev, Playback{URL: p.target, Captions: p.captions, Stream: p.stream()})
	}
	if err == nil {
		err = superviseFixture(s.ctx, dev, *p.delivery, p.feed)
	}
	r := p.reached
	if err != nil {
		t.Fatalf("casting a program the device takes as it is: %v", err)
	}

	// An encoder would have written its delivery beside the buffer.
	spool := r.reader.spool.Path()
	entries, err := os.ReadDir(filepath.Dir(spool))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(spool) {
		t.Errorf("the work directory holds %v, want the read's buffer alone", entries)
	}
	spooled, err := os.ReadFile(spool)
	if err != nil {
		t.Fatal(err)
	}
	if len(spooled) == 0 || !bytes.Equal(dev.served, spooled) {
		t.Errorf("the device was served %d bytes, want exactly the %d the read buffered", len(dev.served), len(spooled))
	}
	if copied := evidence(r, nil, false).Copied; !copied.Video || !copied.Audio {
		t.Errorf("the outcome says the read copied %s, want both halves", copied)
	}
}

func TestEachAttemptSelectsItsOwnTranscription(t *testing.T) {
	var selected []string
	var started []string
	c := Cast{Transcription: func(_ context.Context, language string) Subtitles {
		selected = append(selected, language)
		return func(context.Context) Transcript {
			started = append(started, language)
			return nil
		}
	}}
	first := testPreparation(t.Context(), c, recovery.Attempt{Rendition: source.Rendition{AudioLanguage: "en"}})
	defer first.releases.release()
	second := testPreparation(t.Context(), c, recovery.Attempt{Rendition: source.Rendition{AudioLanguage: "fr"}})
	defer second.releases.release()
	if len(started) != 0 {
		t.Fatal("opening an attempt started recognition before a PCM read")
	}
	second.subtitles(t.Context())
	first.subtitles(t.Context())
	if len(selected) != 2 || selected[0] != "en" || selected[1] != "fr" || len(started) != 2 || started[0] != "fr" || started[1] != "en" {
		t.Fatalf("attempt selections leaked: selected=%v started=%v", selected, started)
	}
}

func reachAttemptState(t *testing.T, m *fsm.FSM, state string) {
	t.Helper()
	if state == preparing {
		return
	}
	path := []attemptEvent{inspectionStarted}
	if state != inspecting {
		path = append(path, inspectionFinished)
	}
	if state != inspecting && state != planning {
		path = append(path, planFinished)
	}
	if state == producing {
		path = append(path, productionStarted)
	} else if state != inspecting && state != planning && state != outputPlanned {
		path = append(path, presentationReady)
	}
	if state == startingPlayback || state == playingBack || state == draining {
		path = append(path, playbackRequested)
	}
	if state == playingBack || state == draining {
		path = append(path, playbackStarted)
	}
	if state == draining {
		path = append(path, deliverySettled)
	}
	if state == stopping || state == attemptFinished {
		path = append(path, attemptCancelled)
	}
	if state == attemptFinished {
		path = append(path, cleanupFinished)
	}
	for _, event := range path {
		if err := fireAttempt(t.Context(), m, event); err != nil {
			t.Fatal(err)
		}
	}
	if !m.Is(state) {
		t.Fatalf("reached %s, want %s", m.Current(), state)
	}
}

func TestReadyAttemptRequiresPlaybackRequest(t *testing.T) {
	c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})
	reachAttemptState(t, c.machine, outputPlanned)
	c.move(presentationReady)
	if !c.machine.Is(ready) {
		t.Fatalf("preparation reached %s, want ready", c.machine.Current())
	}
	if err := fireAttempt(t.Context(), c.machine, playbackStarted); err == nil || !c.machine.Is(ready) {
		t.Fatalf("playback started without a request: state=%s error=%v", c.machine.Current(), err)
	}
	c.move(playbackRequested)
	if !c.machine.Is(startingPlayback) {
		t.Fatalf("playback request reached %s, want starting-playback", c.machine.Current())
	}
}

func TestPreparationStopsAtTheFailedStage(t *testing.T) {
	for _, failed := range []string{"inspect", "plan", "produce"} {
		t.Run(failed, func(t *testing.T) {
			failure := errors.New(failed + " failed")
			var stages []string
			c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})
			c.inspect = func() (inspectedPresentation, error) {
				stages = append(stages, "inspect")
				if failed == "inspect" {
					return inspectedPresentation{}, failure
				}
				return inspectedPresentation{kind: compose.Remux}, nil
			}
			c.plan = func(p inspectedPresentation) (plannedPresentation, error) {
				stages = append(stages, "plan")
				if failed == "plan" {
					return plannedPresentation{inspectedPresentation: p}, failure
				}
				return plannedPresentation{inspectedPresentation: p}, nil
			}
			c.produce = func(p plannedPresentation) (presentation, error) {
				stages = append(stages, "produce")
				return p.presentation, failure
			}
			_, err := c.run()
			if !errors.Is(err, failure) || stages[len(stages)-1] != failed || !c.machine.Is(attemptFinished) {
				t.Fatalf("stages=%v state=%s error=%v", stages, c.machine.Current(), err)
			}
		})
	}
}

func TestCancellationDuringPlanningJoinsLateAcquisitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})
		entered := make(chan struct{})
		lateFailure := errors.New("planner release failed")
		var cleaned, produced bool
		c.inspect = func() (inspectedPresentation, error) {
			return inspectedPresentation{kind: compose.Remux}, nil
		}
		c.plan = func(p inspectedPresentation) (plannedPresentation, error) {
			close(entered)
			<-c.ctx.Done()
			c.releases.push(func() error { cleaned = true; return lateFailure })
			return plannedPresentation{inspectedPresentation: p}, c.ctx.Err()
		}
		c.produce = func(p plannedPresentation) (presentation, error) {
			produced = true
			return p.presentation, nil
		}
		done := make(chan error, 1)
		go func() { _, err := c.run(); done <- err }()
		<-entered
		c.stop(context.Canceled)
		err := <-done
		if !cleaned || produced || !errors.Is(err, context.Canceled) || !errors.Is(err, lateFailure) || !c.machine.Is(attemptFinished) {
			t.Fatalf("cleaned=%v produced=%v state=%s error=%v", cleaned, produced, c.machine.Current(), err)
		}
	})
}

func TestCancelledPreparationReleasesLateAcquisitions(t *testing.T) {
	c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})

	entered := make(chan struct{})
	lateFailure := errors.New("preparation failed during cancellation")
	cleaned := false
	c.preparation = c.launch(presentationReady, func() error {
		close(entered)
		<-c.ctx.Done()
		c.releases.push(func() error { cleaned = true; return nil })
		return lateFailure
	})
	<-entered
	c.stop(context.Canceled)
	c.loop()
	_, err := c.finish()
	interrupted, ok := errors.AsType[*recovery.Interrupted](err)
	if !ok || !errors.Is(interrupted.Cause, context.Canceled) || !errors.Is(interrupted.Failure, lateFailure) {
		t.Fatalf("interruption lost its separate failure: %v", err)
	}
	if !cleaned || !c.machine.Is(attemptFinished) || !errors.Is(err, lateFailure) {
		t.Fatalf("cleaned=%v state=%v error=%v", cleaned, c.machine.Current(), err)
	}
}

type coordinatorDevice struct {
	fakeDevice
	start func(context.Context) error
	wait  func(context.Context) error
}

func (d *coordinatorDevice) Play(ctx context.Context, _ Playback) error {
	return d.start(ctx)
}
func (d *coordinatorDevice) AwaitEnd(ctx context.Context) error { return d.wait(ctx) }

func TestCancellationDuringPlayJoinsCommandWithoutStartingPlayback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan struct{})
		returned := make(chan struct{})
		var awaited atomic.Bool
		d := &coordinatorDevice{
			start: func(ctx context.Context) error { close(entered); <-ctx.Done(); close(returned); return nil },
			wait:  func(context.Context) error { awaited.Store(true); return nil },
		}
		c := newCoordinator(t.Context(), Cast{Device: d}, recovery.Attempt{})
		reachAttemptState(t, c.machine, producing)

		c.preparation = c.launch(presentationReady, func() error {
			c.presentation = presentation{target: &url.URL{Scheme: "https", Host: "example.test"}, contentType: media.MP4, source: true, readiness: sourceEligible}
			return nil
		})
		done := make(chan error, 1)
		go func() { c.loop(); _, err := c.finish(); done <- err }()
		<-entered
		c.stop(context.Canceled)
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("result=%v", err)
		}
		select {
		case <-returned:
		default:
			t.Fatal("Play not joined")
		}
		if awaited.Load() {
			t.Fatal("late Play result started playback observation")
		}
		if !c.machine.Is(attemptFinished) {
			t.Fatalf("state=%v", c.machine.Current())
		}
	})
}

func TestProductionCompletionDoesNotEndPlayingAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := took(t, blocking)
		close(p.buf.reader.done)
		playback := make(chan struct{})
		p.dev = observedDevice{wait: func(context.Context) error { <-playback; return nil }}
		c := newCoordinator(t.Context(), Cast{Device: p.dev}, recovery.Attempt{})

		d := p.delivery()
		reachAttemptState(t, c.machine, playingBack)
		c.presentation = presentation{delivery: &d, feed: feed{buffered: p.buf}}
		c.watchProduction()
		c.observePlayback()
		done := make(chan error, 1)
		go func() { c.loop(); _, err := c.finish(); done <- err }()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("production ended playback: %v", err)
		default:
		}
		close(playback)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestLateWorkerFailureSurvivesSuccessfulDelivery(t *testing.T) {
	c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})

	reachAttemptState(t, c.machine, draining)
	failure := errors.New("device failed while stopping")
	c.launch(playbackEnded, func() error { <-c.ctx.Done(); return failure })
	c.launch(drainElapsed, func() error { return nil })
	c.loop()
	_, err := c.finish()
	if !errors.Is(err, failure) {
		t.Fatalf("late failure lost: %v", err)
	}
}

func TestStoppingSignalsAllPublicationsBeforeJoining(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})

		reachAttemptState(t, c.machine, stopping)
		second := make(chan struct{})
		firstJoined := false
		cleanupFailure := errors.New("storage close failed")
		c.releases.push(func() error {
			if !firstJoined {
				t.Error("storage released before requests joined")
			}
			return cleanupFailure
		})
		c.releases.publication(func() error { <-second; firstJoined = true; return nil })
		c.releases.publication(func() error { close(second); return nil })
		_, err := c.finish()
		if !c.machine.Is(attemptFinished) || !errors.Is(err, cleanupFailure) {
			t.Fatalf("state=%v err=%v", c.machine.Current(), err)
		}
	})
}

func TestFailedProductionCannotStartPlayback(t *testing.T) {
	p := took(t, blocking)
	failure := errors.New("encoder failed after readiness")
	p.buf.reader.err = failure
	close(p.buf.reader.done)
	var started atomic.Bool
	dev := &coordinatorDevice{
		start: func(context.Context) error { started.Store(true); return nil },
		wait:  blocking,
	}
	c := newCoordinator(t.Context(), Cast{Device: dev}, recovery.Attempt{})
	reachAttemptState(t, c.machine, producing)

	d := p.delivery()
	c.preparation = c.launch(presentationReady, func() error {
		c.presentation = presentation{target: &url.URL{Scheme: "http", Host: "localhost"}, contentType: media.MP4, readiness: outputValidated, delivery: &d, feed: feed{buffered: p.buf}}
		return nil
	})
	c.loop()
	_, err := c.finish()
	if started.Load() {
		t.Fatal("started playback with a known producer failure")
	}
	if !errors.Is(err, failure) {
		t.Fatalf("lost producer failure: %v", err)
	}
}

func TestStorageFailureCancelsPreparationAndJoinsLateAcquisitions(t *testing.T) {
	c := newCoordinator(t.Context(), Cast{}, recovery.Attempt{})
	failure := errors.New("storage exhausted during preparation")
	cleaned := false
	c.inspect = func() (inspectedPresentation, error) {
		p := newPreparation(c.ctx, Cast{}, recovery.Attempt{}, c.releases, c.failures)
		p.watchStorage(func(context.Context) error { return failure })
		<-c.ctx.Done()
		// An acquisition already in progress can finish after cancellation.
		c.releases.push(func() error { cleaned = true; return nil })
		return inspectedPresentation{}, c.ctx.Err()
	}
	_, err := c.run()
	if !errors.Is(err, failure) || !cleaned || !c.machine.Is(attemptFinished) {
		t.Fatalf("failure=%v cleaned=%v state=%s", err, cleaned, c.machine.Current())
	}
}

func TestCancelledAttemptDoesNotSelectPreparationServices(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	selected := false
	c := newCoordinator(ctx, Cast{Transcription: func(context.Context, string) Subtitles {
		selected = true
		return nil
	}}, recovery.Attempt{})
	_, err := c.run()
	interrupted, ok := errors.AsType[*recovery.Interrupted](err)
	if !ok || interrupted.Failure != nil {
		t.Fatalf("clean cancellation became a failure: %v", err)
	}
	if selected || !errors.Is(err, context.Canceled) {
		t.Fatalf("selected=%v error=%v", selected, err)
	}
}
