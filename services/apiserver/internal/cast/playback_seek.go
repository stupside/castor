package cast

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/internal/castid"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"google.golang.org/protobuf/proto"
)

type playbackSeek struct {
	ctx    context.Context
	at     time.Duration
	paused bool
	done   chan error
}
type playbackResult struct {
	ended *castorv1.Ended
	err   error
}

// playbackRun stages one media pipeline on the same leased device. Preparation
// cannot replace the current picture until the new output is ready for Play.
type playbackRun struct {
	*playback
	base            time.Duration
	paused          bool
	offered, accept chan struct{}
	ready           chan error
	done            chan playbackResult
	cancel          context.CancelFunc
	show            func(*castorv1.CastStatus)
	active          atomic.Bool
	status          atomic.Pointer[castorv1.CastStatus]
	once            sync.Once
	previous        *loadedPlayback
	head            device.Playhead
}

func (p *playback) canReload(loaded *loadedPlayback) bool {
	return p.reload && p.requests != nil && loaded != nil && loaded.media.Play.GetPlaylist() != nil
}

func (p *playback) seek(ctx context.Context, at time.Duration) (err error) {
	loaded := p.loaded.Load()
	if loaded == nil {
		return errors.New("the receiver is not playing yet")
	}
	head, err := p.Device.Position(ctx)
	if err != nil {
		return err
	}
	if !head.Paused && device.ControlsOf(p.Device).Pause {
		// Hold the picture before either seeking or preparing another portion.
		// A receiver that acknowledges SEEK but resets must not replay that old
		// portion while its position is checked and the replacement is prepared.
		defer func() {
			if p.loaded.Load() == loaded {
				restore, cancel := context.WithTimeout(p.lifetime, 5*time.Second)
				defer cancel()
				err = errors.Join(err, p.Device.Resume(restore))
			}
		}()
		if err := p.Device.Pause(ctx); err != nil {
			return err
		}
		slog.DebugContext(ctx, "playback held for seek", "at", at)
	}
	local := at - loaded.base
	outside := local < 0 || head.Seekable != nil && (local < head.Seekable.Start || local > head.Seekable.End)
	if !p.canReload(loaded) || !outside {
		err = device.Seek(ctx, p.Device, local)
		if err == nil || !p.canReload(loaded) || ctx.Err() != nil {
			return err
		}
		slog.DebugContext(ctx, "receiver seek requires source preparation", "at", at, "error", err)
	}
	request := &playbackSeek{ctx: ctx, at: at, paused: head.Paused, done: make(chan error, 1)}
	select {
	case p.requests <- request:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	select {
	case err := <-request.done:
		return err
	case <-p.lifetime.Done():
		return context.Cause(p.lifetime)
	}
}

func (p *playback) drive(ctx context.Context, id string, start time.Duration, show func(*castorv1.CastStatus), run func(context.Context, string, time.Duration, device.Device, func(*castorv1.CastStatus)) (*castorv1.Ended, error)) (*castorv1.Ended, error) {
	launch := func(key string, base time.Duration, paused bool) *playbackRun {
		life, cancel := context.WithCancel(ctx)
		r := &playbackRun{playback: p, base: base, paused: paused, offered: make(chan struct{}), accept: make(chan struct{}), ready: make(chan error, 1), done: make(chan playbackResult, 1), cancel: cancel, show: show}
		go func() {
			ended, err := run(life, key, base, r, func(s *castorv1.CastStatus) {
				r.status.Store(proto.CloneOf(s))
				if r.active.Load() {
					show(s)
				}
			})
			r.done <- playbackResult{ended, err}
		}()
		return r
	}
	current := launch(id, start, false)
	current.active.Store(true)
	close(current.accept)
	defer func() { current.cancel() }()
	for {
		select {
		case result := <-current.done:
			return result.ended, result.err
		case <-ctx.Done():
			current.cancel()
			result := <-current.done
			return result.ended, result.err
		case request := <-p.requests:
			if request.ctx.Err() != nil {
				request.done <- context.Cause(request.ctx)
				continue
			}
			candidate := launch(castid.New(), request.at, request.paused)
			candidate.previous = p.loaded.Load()
			candidate.head, _ = p.Device.Position(request.ctx)
			var failure error
			var early *playbackResult
			select {
			case <-candidate.offered:
				close(candidate.accept)
				select {
				case failure = <-candidate.ready:
				case result := <-candidate.done:
					early = &result
					failure = playbackPreparationError(result)
				case <-ctx.Done():
					failure = context.Cause(ctx)
				}
			case result := <-candidate.done:
				early = &result
				failure = playbackPreparationError(result)
			case <-request.ctx.Done():
				failure = context.Cause(request.ctx)
			case <-ctx.Done():
				failure = context.Cause(ctx)
			}
			if failure != nil {
				candidate.cancel()
				if early == nil {
					<-candidate.done
				}
				request.done <- failure
				continue
			}
			// The new LOAD was accepted. Old resources and observers are joined
			// before this request returns, but receiver ownership is never released.
			current.active.Store(false)
			current.cancel()
			<-current.done
			current = candidate
			current.active.Store(true)
			if state := current.status.Load(); state != nil {
				show(state)
			}
			slog.InfoContext(ctx, "playback prepared from requested source position", "at", request.at, "paused", request.paused)
			request.done <- nil
		}
	}
}

func playbackPreparationError(result playbackResult) error {
	if result.err != nil {
		return result.err
	}
	if failed := result.ended.GetFailed(); failed != nil {
		return errors.New(failed.Message)
	}
	return errors.New("the source did not produce playback at the requested position")
}

func (r *playbackRun) Play(ctx context.Context, media device.Playback) error {
	r.once.Do(func() { close(r.offered) })
	select {
	case <-r.accept:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	err := r.playAt(ctx, media, r.base)
	if err == nil && r.paused {
		err = r.Device.Pause(ctx)
	}
	if err != nil && r.previous != nil {
		// The old publication is still alive until commit, so a refused LOAD
		// can restore it without re-reading or dropping the active cast.
		restore, cancel := context.WithTimeout(r.lifetime, 10*time.Second)
		old := r.previous
		restored := r.Device.Play(restore, old.media)
		if restored == nil {
			if device.ControlsOf(r.Device).Seek {
				restored = device.Seek(restore, r.Device, r.head.At)
			}
			if r.head.Paused {
				restored = errors.Join(restored, r.Device.Pause(restore))
			}
			r.loaded.Store(old)
			r.rewatch()
		}
		cancel()
		err = errors.Join(err, restored)
	}
	select {
	case r.ready <- err:
	default:
	}
	return err
}

func (p *playback) rewatch() {
	p.watch.Lock()
	defer p.watch.Unlock()
	if p.changed != nil {
		close(p.changed)
	}
	p.changed = make(chan struct{})
}

// A replacement LOAD changes the receiver's media-session watch. Reattach
// observers after a rollback as well as after a successful reposition.
func (p *playback) AwaitEnd(ctx context.Context) error {
	for {
		p.watch.Lock()
		if p.changed == nil {
			p.changed = make(chan struct{})
		}
		changed := p.changed
		p.watch.Unlock()
		watch, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- p.Device.AwaitEnd(watch) }()
		select {
		case err := <-done:
			cancel()
			return err
		case <-changed:
			cancel()
			<-done
		case <-ctx.Done():
			cancel()
			<-done
			return context.Cause(ctx)
		}
	}
}
