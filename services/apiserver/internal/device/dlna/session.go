package dlna

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync/atomic"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/soap"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// actionTimeout keeps an unresponsive device from wedging an AVTransport action.
const actionTimeout = 10 * time.Second

// session is a device driven over AVTransport with a generic client, since goupnp av1 hardcodes the :1 URN.
type session struct {
	transport goupnp.ServiceClient
	caps      *mediav1.Capabilities
	// sinks is how the device names each container it accepts.
	sinks map[mediav1.Container]sinkEntry
	// ticked is a playhead seen to move since the last Play, before which a 0:00:00 says nothing.
	ticked atomic.Bool
}

func (s *session) Capabilities() *mediav1.Capabilities { return s.caps }

// name is the device's FriendlyName, else its location.
func (s *session) name() string {
	var friendly, location string
	if s.transport.RootDevice != nil {
		friendly = s.transport.RootDevice.Device.FriendlyName
	}
	if s.transport.Location != nil {
		location = s.transport.Location.String()
	}
	return cmp.Or(friendly, location, "DLNA device")
}

var _ device.Device = (*session)(nil)

const (
	transportLockedRetries = 5
	transportLockedDelay   = 300 * time.Millisecond
)

// Play hands over one video resource, announcing the caption to show beside it when there is one.
func (s *session) Play(ctx context.Context, playback device.Playback) error {
	entry, ok := s.sinks[playback.Container()]
	if !ok {
		return fmt.Errorf("the device does not advertise container %v", playback.Container())
	}
	metadata, err := buildDIDLMetadata(entry, playback)
	if err != nil {
		return fmt.Errorf("building DIDL-Lite metadata: %w", err)
	}
	slog.DebugContext(ctx, "DIDL metadata", "xml", metadata)

	setURI := &struct {
		InstanceID         string
		CurrentURI         string
		CurrentURIMetaData string
	}{"0", playback.URL.String(), metadata}
	set := func() error {
		return retryTransportLocked(ctx, func() error { return s.action(ctx, "SetAVTransportURI", setURI) })
	}
	err = set()
	// A renderer may refuse a new stream while paused, so it is stopped first.
	if upnpError(err, transitionUnavailable) && s.Stop(ctx) == nil {
		err = set()
	}
	if err != nil {
		return fmt.Errorf("setting transport URI: %w", err)
	}
	s.ticked.Store(false)

	play := &struct {
		InstanceID string
		Speed      string
	}{"0", "1"}
	if err := retryTransportLocked(ctx, func() error {
		return s.action(ctx, "Play", play)
	}); err != nil {
		// A renderer may start playing without acknowledging Play before its deadline.
		// Do not tear down that stream, or resend Play, if its state confirms the handover.
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			if s.playingStream(ctx, playback.URL) {
				slog.InfoContext(ctx, "device confirmed playback after Play timed out", "device", s.name())
				return nil
			}
			// A cancel during the confirmation is seen only here.
			err = errors.Join(err, context.Cause(ctx))
		}
		return fmt.Errorf("starting playback: %w", err)
	}
	return nil
}

// playingStream confirms both playback and the resource, since an older stream playing is not success.
func (s *session) playingStream(ctx context.Context, streamURL *url.URL) bool {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	info, err := s.transportInfo(ctx)
	if err != nil || info.CurrentTransportState != statePlaying {
		return false
	}
	var response struct{ CurrentURI string }
	err = s.transport.SOAPClient.PerformActionCtx(ctx, s.transport.Service.ServiceType,
		"GetMediaInfo", &struct{ InstanceID string }{"0"}, &response)
	return err == nil && response.CurrentURI == streamURL.String() && ctx.Err() == nil
}

// shownCaption is the one caption a renderer is told of: the default, else the first.
func shownCaption(captions []*mediav1.DeviceCommand_Caption) *mediav1.DeviceCommand_Caption {
	for _, c := range captions {
		if c.GetDefault() {
			return c
		}
	}
	if len(captions) > 0 {
		return captions[0]
	}
	return nil
}

func (s *session) Pause(ctx context.Context) error {
	if err := s.action(ctx, "Pause", &struct{ InstanceID string }{"0"}); err != nil {
		return fmt.Errorf("pausing playback: %w", err)
	}
	return nil
}

func (s *session) Resume(ctx context.Context) error {
	if err := s.action(ctx, "Play", &struct{ InstanceID, Speed string }{"0", "1"}); err != nil {
		return fmt.Errorf("resuming playback: %w", err)
	}
	return nil
}

func retryTransportLocked(ctx context.Context, do func() error) error {
	var err error
	for attempt := range transportLockedRetries {
		err = do()
		if !upnpError(err, transportLocked) {
			return err
		}
		slog.DebugContext(ctx, "transport locked, retrying action", "attempt", attempt+1)
		select {
		case <-ctx.Done():
			return errors.Join(err, context.Cause(ctx))
		case <-time.After(transportLockedDelay):
		}
	}
	return err
}

// AVTransport error codes.
const (
	transitionUnavailable = 701
	transportLocked       = 705
)

func upnpError(err error, code int) bool {
	fault, ok := errors.AsType[*soap.SOAPFaultError](err)
	return ok && fault.Detail.UPnPError.Errorcode == code
}

// action performs a SOAP action namespaced to the service version the device published.
func (s *session) action(ctx context.Context, name string, request any) error {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	err := s.transport.SOAPClient.PerformActionCtx(
		ctx, s.transport.Service.ServiceType, name, request, nil)
	// goupnp formats HTTP errors with %v, so preserve the context cause structurally.
	if err != nil && ctx.Err() != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	return err
}

const transportQuery = "GetTransportInfo"

// idlePolls is how many polls in a row a device may sit stopped with nothing playing before it is taken to have refused the stream.
const idlePolls = 15

// transportState is AVTransport's TransportState.
type transportState string

const (
	statePlaying   transportState = "PLAYING"
	statePaused    transportState = "PAUSED_PLAYBACK"
	stateRecording transportState = "RECORDING"
	stateStopped   transportState = "STOPPED"
	stateNoMedia   transportState = "NO_MEDIA_PRESENT"
)

func (t transportState) active() bool {
	return t == statePlaying || t == statePaused || t == stateRecording
}

// idle is a renderer holding no stream.
func (t transportState) idle() bool { return t == stateStopped || t == stateNoMedia }

// transportWatch holds the one thing that ends a cast from the device's side.
type transportWatch struct {
	active bool
	idle   int
}

// observe reports whether the cast is over: the device stopped after playing, or never played at all.
func (w *transportWatch) observe(state transportState) bool {
	switch {
	case state.active():
		w.active = true
		w.idle = 0
	case state.idle():
		w.idle++
		return w.active || w.refused()
	default:
		w.idle = 0
	}
	return false
}

func (w *transportWatch) refused() bool { return !w.active && w.idle >= idlePolls }

// AwaitEnd polls AVTransport because UPnP families share no dependable event subscription.
func (s *session) AwaitEnd(ctx context.Context) error {
	return awaitTransportEnd(ctx, s.name(), s.transportInfo)
}

// awaitTransportEnd folds the AVTransport state machine over the shared poll loop.
func awaitTransportEnd(ctx context.Context, name string, poll func(context.Context) (transportInfo, error)) error {
	var watch transportWatch
	err := device.AwaitPolledEnd(ctx, name, transportQuery,
		func(ctx context.Context) (bool, error) {
			info, err := poll(ctx)
			if err != nil {
				return false, err
			}
			over := watch.observe(info.CurrentTransportState)
			// A status read before the end may be left from an earlier playback.
			if over && info.CurrentTransportStatus == "ERROR_OCCURRED" {
				return false, &device.PlaybackError{Err: fmt.Errorf("the device went %s with transport status %s", info.CurrentTransportState, info.CurrentTransportStatus)}
			}
			return over, nil
		})
	if err == nil && watch.refused() {
		return fmt.Errorf("the device refused the stream: it stayed stopped for %d polls without playing", idlePolls)
	}
	return err
}

type transportInfo struct {
	CurrentTransportState  transportState
	CurrentTransportStatus string
}

func (s *session) transportInfo(ctx context.Context) (transportInfo, error) {
	var response transportInfo
	err := s.transport.SOAPClient.PerformActionCtx(ctx, s.transport.Service.ServiceType,
		transportQuery, &struct{ InstanceID string }{"0"}, &response)
	return response, err
}

// Stop ends whatever the device is playing.
func (s *session) Stop(ctx context.Context) error {
	err := s.action(ctx, "Stop", &struct{ InstanceID string }{"0"})
	// A renderer may refuse Stop while it holds nothing, which is stopped already.
	if upnpError(err, transitionUnavailable) {
		ctx, cancel := context.WithTimeout(ctx, actionTimeout)
		defer cancel()
		if info, _ := s.transportInfo(ctx); info.CurrentTransportState.idle() {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("stopping playback: %w", err)
	}
	return nil
}

func (s *session) Close() error {
	return nil
}
